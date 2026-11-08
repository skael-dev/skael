A worker runs evaluations. The server holds no Docker socket and no LLM key, so evaluations run only on workers. Settings → Workers → Add a worker creates a key and shows the command to start one.

Invalid durations and numbers stop the worker at start with the variable named.

### Running the worker in a container

With the docker driver, the worker starts sandbox containers as siblings through a mounted `/var/run/docker.sock`. Two rules follow, and both apply to the docker driver only:

- Bind-mount `WORKER_RUN_ROOT` at the same path on both sides, for example `-v /var/lib/skael/run:/var/lib/skael/run`. Session workspaces are bind-mounted into each sandbox, and the host daemon resolves that path. The worker refuses to start in a container without it.
- Give credentials as environment variables, not as a mounted directory. A Claude subscription works in a container through `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`.
