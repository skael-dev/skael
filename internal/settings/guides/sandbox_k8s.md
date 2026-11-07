Grant the worker's service account only `deploy/kubernetes/worker-rbac.yaml`, and put nothing but session pods in the namespace: `pods/exec` is the driver's whole privilege, and whoever holds it can run commands in any pod there.

The driver uses a published image and builds none. The default is the image the release publishes, tagged by its base version and never by the release version, so a release does not change the eval environment.
