A worker runs evaluations. The server calls no LLM and holds no Docker socket, so evaluations run only on workers. Evaluation → Add a worker creates a key and shows the command to start one. A worker needs only `SKAEL_ENDPOINT` and `SKAEL_API_KEY`: provider keys stay on the server and reach the worker with each job.

Invalid durations and numbers stop the worker at start with the variable named.

### Running the worker in a container

With the docker driver, the worker starts sandbox containers as siblings through a mounted `/var/run/docker.sock`. Bind-mount `WORKER_RUN_ROOT` at the same path on both sides, for example `-v /var/lib/skael/run:/var/lib/skael/run`. Session workspaces are bind-mounted into each sandbox, and the host daemon resolves that path. The worker refuses to start in a container without it.
