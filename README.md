deployment-reaper - A Kubernetes service for the automatic restart of old pods.
==========

**Please Note**: This service has not yet been tested for production use. Use at your own peril.

deployment-reaper is a service designed to automate the restarting of pods that
have run longer than a designated time.

Why deployment-reaper?
----------

deployment-reaper works differently than most pod-reaping services by safely
restarting pods using the same method of restarting Kubernetes 1.15 implemented
via their `kubectl rollout restart` feature. By modifying a pods deployment, 
Kubernetes can safely schedule the restart of an entire deployment of pods
(lets face it, most of a deployment's pods are almost always close in age).

Common Use Cases
----------

* Does your application have leaky RAM? Give it a restart.
* Cleanup old temporary on-local-disk data.

Monitoring Deployments
----------

deployment-reaper uses labels attached to deployments to determine both 
A) if a deployment should be monitored or
B) the maximum age of a deployments pods.


```yaml
  labels:
    reaper.kubernetes.io/managed: "true"
    reaper.kubernetes.io/max-age: 1h
```

Usage
-----

`./deployment-reaper --help`

```
Usage: deployment-reaper --interval="60s" --backoff-period="60s" --default-max-age=STRING --namespace=STRING --managed-label="reaper.kubernetes.io/managed" --max-age-label="reaper.kubernetes.io/max-age" --restart-label="reaper.kubernetes.io/restarted-on"

A Kubernetes service for the automatic restart of old pods.

Flags:
      --help                                                 Show context-sensitive help.
      --kube-config="~/.kube/config"                         The kubeconfig used to authenticate with Kubernetes ($KUBECONFIG).
  -v, --verbose=INT                                          Tweak the verbosity of the logs.
  -i, --interval="60s"                                       How often a reaping cycle should occur.
  -b, --backoff-period="60s"                                 The duration between the time a deployment is restarted and allowed to be restarted again.
  -a, --default-max-age=STRING                               The default maximum age of a container if no max-age label is provided.
  -n, --namespace=STRING                                     The namespace this service runs in ($NAMESPACE).
      --managed-label="reaper.kubernetes.io/managed"         The name of a label that declares a deployment should be managed.
      --max-age-label="reaper.kubernetes.io/max-age"         The name of a label that declares the maximum age of a deployment.
      --restart-label="reaper.kubernetes.io/restarted-on"    The name of a pod annotation added/modified that restarts the deployment.
```

Required Privileges
----------

Deployment-reaper requires RBAC permissions at both the Cluster and Namespace 
level. The service requires access to get/list all pods in all namespaces, as well
as modify deployments at the cluster scope. The following RBAC examples can be
used for granting the proper permissions.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: deployment-reaper
  namespace: kube-system
rules:
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "list", "update", "create"]
```

```yaml
kind: ClusterRole
metadata:
  name: deployment-reaper
rules:
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["get", "list"]
- apiGroups: ["extensions", "apps" ]
  resources: ["deployments"]
  verbs: ["get", "list", "update"]
```

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: deployment-reaper
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: deployment-reaper
subjects:
- kind: ServiceAccount
  name: deployment-reaper
  namespace: kube-system
```

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: deployment-reaper
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: deployment-reaper
subjects:
- kind: ServiceAccount
  name: deployment-reaper
  namespace: kube-system
```