A new install with no `STORAGE_PATH` stores archives in Postgres. Skill archives are at most 10 MB, so one Postgres backup then holds everything. An install that started with a directory keeps it.

Set `STORAGE_PATH=s3://bucket/prefix` for S3, MinIO, R2 or Spaces. A change of store does not move existing archives.
