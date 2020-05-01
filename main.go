package main

import (
	"context"
	"github.com/alecthomas/kong"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type CLI struct {
	KubeConfig     string `type:"path" default:"~/.kube/config" env:"KUBECONFIG" help:"The kubeconfig used to authenticate with Kubernetes."`
	Verbosity      int    `name:"verbose" type:"counter" short:"v" help:"Tweak the verbosity of the logs." `
	Interval       string `default:"60s" short:"i" required:"true" help:"How often a reaping cycle should occur."`
	BackoffPeriod  string `required:"true" short:"b" default:"60s" help:"The duration between the time a deployment is restarted and allowed to be restarted again."`
	DefaultMaxAge  string `required:"true" short:"a" help:"The default maximum age of a container if no max-age label is provided."`
	Namespace      string `env:"NAMESPACE" short:"n" required:"true" help:"The namespace this service runs in."`
	ManagedLabel   string `required:"true" default:"reaper.kubernetes.io/managed" help:"The name of a label that declares a deployment should be managed."`
	MaxAgeLabel    string `required:"true" default:"reaper.kubernetes.io/max-age" help:"The name of a label that declares the maximum age of a deployment."`
	RestartLabel   string `required:"true" default:"reaper.kubernetes.io/restarted-on" help:"The name of a pod annotation added/modified that restarts the deployment."`
	PodName        string `env:"HOSTNAME" required:"true" hidden:"true" help:"The podname of the instance the service runs from."`
	DeploymentName string `env:"PUBLICHOST" required:"true" hidden:"true" default:"local" help:"The name of the deployment, used for leader leasing."`
}

const (
	ctxLogger        = "logger"
	ctxKubectl       = "kubectl"
	ctxDefaultMaxAge = "defaultMaxAge"
	ctxManagedLabel  = "managedLabel"
	ctxMaxAgeLabel   = "maxAgeLabel"
	ctxRestartLabel  = "restartLabel"
	ctxInterval      = "interval"
	ctxBackoffPeriod = "backoffPeriod"
)

func exitHandler(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		log.Info("Received termination, signaling shutdown")
		cancel()
	}()

	return ctx
}

func createKubectl(kubeConfig string) (*kubernetes.Clientset, error) {
	var config *rest.Config
	var err error

	config, err = rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
		if err != nil {
			return nil, err
		}
	}

	kube, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return kube, nil
}

func main() {
	var cli CLI
	kong.Parse(&cli,
		kong.Name("deployment-reaper"),
		kong.Description("A Kubernetes service for the automatic restart of old pods."),
		kong.UsageOnError(),
		kong.Configuration(kong.JSON, "./.config.json"),
	)

	logger := SetupLogger(cli.Verbosity)

	kubectl, err := createKubectl(cli.KubeConfig)
	if err != nil {
		logger.WithError(err).Fatal("could not connect to kubernetes")
	}

	defaultMaxAge, err := time.ParseDuration(cli.DefaultMaxAge)
	if err != nil {
		log.WithError(err).Fatal("could not parse --age")
	}

	interval, err := time.ParseDuration(cli.Interval)
	if err != nil {
		log.WithError(err).Fatal("could not parse --interval")
	}

	backoffPeriod, err := time.ParseDuration(cli.BackoffPeriod)
	if err != nil {
		log.WithError(err).Fatal("could not parse --backoff-period")
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, ctxLogger, logger)
	ctx = context.WithValue(ctx, ctxKubectl, kubectl)
	ctx = context.WithValue(ctx, ctxDefaultMaxAge, defaultMaxAge)
	ctx = context.WithValue(ctx, ctxManagedLabel, cli.ManagedLabel)
	ctx = context.WithValue(ctx, ctxMaxAgeLabel, cli.MaxAgeLabel)
	ctx = context.WithValue(ctx, ctxRestartLabel, cli.RestartLabel)
	ctx = context.WithValue(ctx, ctxInterval, interval)
	ctx = context.WithValue(ctx, ctxBackoffPeriod, backoffPeriod)
	ctx, cancel := context.WithCancel(ctx)
	ctx = exitHandler(ctx)

	logger.Info("reaper has initialized")
	// we use the Lease lock type since edits to Leases are less common
	// and fewer objects in the cluster watch "all Leases".
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cli.DeploymentName,
			Namespace: cli.Namespace,
		},
		Client: kubectl.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cli.PodName,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   60 * time.Second,
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				logger.Info("I'm the leader, night gathers, and now my watch begins. It shall not end until my death.")
				// we're notified when we start - this is where you would
				// usually put your code

				run := func() {
					err := cycle(ctx)
					if err != nil {
						logger.WithError(err).Error("cycle failed")
					}

					logger.Debugf("will run again in %s at %s", cli.Interval, time.Now().Add(interval).String())
				}

				go func() {
					timer := time.NewTicker(interval)
					for {
						select {
						case <-timer.C:
							run()
						case <-ctx.Done():
							return
						}
					}
				}()

				run()
			},
			OnStoppedLeading: func() {
				logger.Infof("leader lost: %s", cli.PodName)
				cancel()
				os.Exit(0)
			},
			OnNewLeader: func(identity string) {
				if identity == cli.PodName {
					// I just got the lock
					return
				}
				logger.Infof("new leader elected: %s", identity)
			},
		},
	})

	<-ctx.Done()
}

func cycle(ctx context.Context) error {
	kubectl := ctx.Value(ctxKubectl).(*kubernetes.Clientset)
	logger := ctx.Value(ctxLogger).(*log.Entry)
	defaultMaxAge := ctx.Value(ctxDefaultMaxAge).(time.Duration)
	managedLabel := ctx.Value(ctxManagedLabel).(string)

	labelSelector := labels.Set(map[string]string{managedLabel: "true"})

	logger.Tracef("attempting to list managed deployments labeled via %s", labelSelector.String())
	deployments, err := kubectl.AppsV1().Deployments("").List(metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})
	if err != nil {
		logger.WithError(err).Debug("list deployments failed")
		return err
	}

	logger.Tracef("%d deployments are being managed", len(deployments.Items))

	if len(deployments.Items) == 0 {
		logger.Debug("no deployments are configured")
		return nil
	}

	for _, deployment := range deployments.Items {
		logger := logger.WithFields(log.Fields{
			"deploymentName":      deployment.Name,
			"deploymentNamespace": deployment.Namespace,
		})
		ctx = context.WithValue(ctx, "logger", logger)
		logger.Tracef("looking at deployment %s", deployment.Name)

		if shouldBackoff(ctx, deployment) {
			logger.Debugf("backing off deployment %s for now", deployment.Name)
			continue
		}

		maxAge, err := getDeploymentMaxAge(ctx, deployment)
		if err != nil {
			logger.WithError(err).Error("could not determine maximum setting for deployment")
			continue
		}

		logger.Tracef("pods should not be allowed to age older than %s", maxAge)

		if maxAge == nil {
			maxAge = &defaultMaxAge
		}

		podSelector := labels.Set(deployment.Spec.Selector.MatchLabels)
		logger.Tracef("attempting to list existing pods via selector %s", podSelector.String())
		pods, err := kubectl.CoreV1().Pods("").List(metav1.ListOptions{
			LabelSelector: podSelector.String(),
		})
		if err != nil {
			logger.WithError(err).Debug("list pods failed")
			return err
		}

		deploymentAged := false
		for _, pod := range pods.Items {
			logger := logger.WithFields(log.Fields{
				"podName": pod.Name,
				"phase":   pod.Status.Phase,
			})
			ctx = context.WithValue(ctx, "logger", logger)

			if pod.Status.Phase != v1.PodRunning {
				logger.Debugf("skipping pod as it's phase != PodRunning", pod.Status.Phase)
				continue
			}

			logger.Tracef("looking at pod %s", pod.Name)

			runtime := getPodRuntime(pod)

			logger.Tracef("pod is aged %s", runtime)

			if runtime > *maxAge {
				logger.Debugf("deployment contains an aged pod: %s", pod.Name)
				deploymentAged = true
			}
		}

		if deploymentAged {
			logger.Infof("restarting deployment %s due to aged pods", deployment.Name)

			err := restartDeployment(ctx, kubectl, deployment)
			if err != nil {
				logger.WithError(err).Error("failed to restart deployment")
				continue
			}
		}
	}

	return nil
}

func shouldBackoff(ctx context.Context, deployment appsv1.Deployment) bool {
	annotationRestartedOn := ctx.Value(ctxRestartLabel).(string)
	backoffPeriod := ctx.Value(ctxBackoffPeriod).(time.Duration)

	for key, val := range deployment.Annotations {
		if key == annotationRestartedOn {
			lastRestartedOn, err := time.Parse(time.RFC3339, val)
			if err != nil {
				continue
			}

			if time.Now().Before(lastRestartedOn.Add(backoffPeriod)) {
				return true
			}
		}
	}

	return false
}

func restartDeployment(ctx context.Context, kubectl *kubernetes.Clientset, deployment appsv1.Deployment) error {
	restartedOn := time.Now().Format(time.RFC3339)
	annotationRestartedOn := ctx.Value(ctxRestartLabel).(string)

	if deployment.ObjectMeta.Annotations == nil {
		deployment.ObjectMeta.Annotations = map[string]string{}
	}

	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}

	deployment.ObjectMeta.Annotations[annotationRestartedOn] = restartedOn
	deployment.Spec.Template.ObjectMeta.Annotations[annotationRestartedOn] = restartedOn

	_, err := kubectl.AppsV1().Deployments(deployment.Namespace).Update(&deployment)

	return err
}

func getPodRuntime(pod v1.Pod) time.Duration {
	return time.Now().Sub(pod.CreationTimestamp.Time)
}

func getDeploymentMaxAge(ctx context.Context, deployment appsv1.Deployment) (*time.Duration, error) {
	maxAgeLabel := ctx.Value(ctxMaxAgeLabel).(string)
	logger := ctx.Value(ctxLogger).(*log.Entry)
	var maxAge *time.Duration

	for key, val := range deployment.Labels {
		if key == maxAgeLabel {
			duration, err := time.ParseDuration(val)
			if err != nil {
				logger.WithError(err).Warnf("could not parse duration %s", val)
				return nil, err
			}

			maxAge = &duration
		}
	}

	return maxAge, nil
}
