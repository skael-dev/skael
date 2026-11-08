These settings are read before the server reads its database, or a wrong value could lock you out of this page. Set them in the environment and restart.

Without `SKAEL_MASTER_KEYS`, the server generates a master key on first start and writes it to `master.key` in the data directory. Back up that file with Postgres. If the key is lost, the server still starts, and this page lists each secret to enter again.
