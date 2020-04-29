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
