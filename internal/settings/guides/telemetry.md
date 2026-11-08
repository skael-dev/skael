The ping tells the skael project how many installs run and which versions they run. It sends one JSON object a day to `https://telemetry.skael.dev/v1/ping`:

```json
{
  "install_id": "a random UUID made at install",
  "version": "0.19.0",
  "postgres_version": "17.2",
  "workers": 2,
  "evals_configured": true
}
```

It sends no user, email, skill, repository, host name or IP address of yours beyond the connection itself. Sample data never counts. Turn it off here or with `TELEMETRY_ENABLED=false`.
