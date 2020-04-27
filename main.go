package main

import (
	"context"
	"github.com/alecthomas/kong"
	log "github.com/sirupsen/logrus"
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
	Interval       int    `default:"5" required:"true" help:"How often a reaping cycle should occur."`
	NodeName       string `env:"NODE_NAME" hidden:"true"`
	HostName       string `env:"HOSTNAME" hidden:"true"`
	DeploymentName string `env:"PUBLICHOST" hidden:"true" default:"local"`
	Age            string `required:"true" help:"The default age of a container if no max-age annotation is provided."`
	Namespace      string `env:"NAMESPACE" required:"true" help:"The namespace this service runs in."`
	ManagedLabel   string `required:"true" default:"lifecycle.kubernetes.io/managed" help:"The name of a label that declares a pod should be managed."`
	MaxAgeLabel    string `required:"true" default:"lifecycle.kubernetes.io/max-age" help:"The name of a label that declares the maximum age of a pod."`
}

const (
	AnnotationRestartedOn = "lifecycle.kubernetes.io/restarted-on"
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
		kong.Name("pod-lifecycle"),
		kong.Description("A service for automatically restarting pods based on a pods age."),
		kong.UsageOnError(),
		kong.Configuration(kong.JSON, "./.config.json"),
	)

	logger := SetupLogger(cli.Verbosity)

	kubectl, err := createKubectl(cli.KubeConfig)
	if err != nil {
		logger.WithError(err).Fatal("could not connect to kubernetes")
	}

	defaultMaxAge, err := time.ParseDuration(cli.Age)
	if err != nil {
		log.WithError(err).Fatal("could not parse --age")
	}

	ctx := context.Background()
	ctx = context.WithValue(ctx, "logger", logger)
	ctx = context.WithValue(ctx, "kubectl", kubectl)
	ctx = context.WithValue(ctx, "defaultMaxAge", defaultMaxAge)
	ctx = context.WithValue(ctx, "managedLabel", cli.ManagedLabel)
	ctx = context.WithValue(ctx, "maxAgeLabel", cli.MaxAgeLabel)
	ctx = exitHandler(ctx)

	// we use the Lease lock type since edits to Leases are less common
	// and fewer objects in the cluster watch "all Leases".
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cli.DeploymentName,
			Namespace: cli.Namespace,
		},
		Client: kubectl.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cli.HostName,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: lock,
		// IMPORTANT: you MUST ensure that any code you have that
		// is protected by the lease must terminate **before**
		// you call cancel. Otherwise, you could have a background
		// loop still running and another process could
		// get elected before your background loop finished, violating
		// the stated goal of the lease.
		ReleaseOnCancel: true,
		LeaseDuration:   60 * time.Second,
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				logger.Info("I'm the leader, starting cycle")
				// we're notified when we start - this is where you would
				// usually put your code

				run := func() {
					err := cycle(ctx)
					if err != nil {
						logger.WithError(err).Error("cycle failed")
					}

					logger.Debugf("will run again in %d seconds at %s", cli.Interval, time.Now().Add(time.Duration(cli.Interval)).String())
				}

				go func() {
					timer := time.NewTicker(time.Second * time.Duration(cli.Interval))
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
				// we can do cleanup here
				logger.Infof("leader lost: %s", cli.HostName)
				os.Exit(0)
			},
			OnNewLeader: func(identity string) {
				// we're notified when new leader elected
				if identity == cli.HostName {
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
	kubectl := ctx.Value("kubectl").(*kubernetes.Clientset)
	logger := ctx.Value("logger").(*log.Entry)
	managedLabel := ctx.Value("managedLabel").(string)

	labelSelector := labels.Set(map[string]string{managedLabel: "true"})

	logger.Trace("attempting to list existing pods")
	pods, err := kubectl.CoreV1().Pods("").List(metav1.ListOptions{
		LabelSelector: labelSelector.String(),
	})
	if err != nil {
		logger.WithError(err).Debug("list pods failed")
		return err
	}

	logger.Tracef("%d pods are being managed", len(pods.Items))

	if len(pods.Items) == 0 {
		logger.Debug("no pods are configured")
		return nil
	}

	for _, pod := range pods.Items {
		logger := logger.WithFields(log.Fields{
			"name":      pod.Name,
			"namespace": pod.Namespace,
		})
		ctx = context.WithValue(ctx, "logger", logger)
		logger.Tracef("looking at pod %s", pod.Name)

		maxAge, err := getPodMaxAge(ctx, pod)
		if err != nil {
			logger.WithError(err).Error("could not determine maximum setting for pod")
			continue
		}
		runtime := getPodRuntime(pod)

		if runtime > *maxAge {
			logger.Infof("restarting pod due to old age %s", runtime.String())
			err := restartPod(kubectl, pod)
			if err != nil {
				logger.WithError(err).Error("failed to restart pod")
				continue
			}
		}
	}

	return nil
}

func restartPod(kubectl *kubernetes.Clientset, pod v1.Pod) error {
	pod.Annotations[AnnotationRestartedOn] = time.Now().String()
	_, err := kubectl.CoreV1().Pods("").Update(&pod)

	return err
}

func getPodRuntime(pod v1.Pod) time.Duration {
	return time.Now().Sub(pod.CreationTimestamp.Time)
}

func getPodMaxAge(ctx context.Context, pod v1.Pod) (*time.Duration, error) {
	maxAgeLabel := ctx.Value("maxAgeLabel").(string)
	defaultMaxAge := ctx.Value("maxAgeLabel").(time.Duration)
	logger := ctx.Value("logger").(*log.Entry)
	maxAge := &defaultMaxAge

	for key, val := range pod.Annotations {
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
