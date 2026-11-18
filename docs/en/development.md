# Development

Development tasks use [Task](https://taskfile.dev/) and Docker.
Run them in `develop/`.

## Development environment

The daemon, hrana, and the mount run in a Linux container. As object storage, MinIO and fake-gcs-server each run in a separate container.

```sh
cd develop
task run         # MinIO (same as task run:minio)
task run:fakegcs # fake-gcs-server
task run:hrana   # MinIO. The daemon and hrana only, without the mount
```

- hrana is reachable from the host at `http://127.0.0.1:8080`

Ctrl-C stops the daemon, hrana, and the mount.

- The yaagwadb binary is cross-built on the host for Linux into `../file-db/build/linux/yaagwadb` (`task build:linux`) and used from the container.
- It is rebuilt only if the source code or the embedded files (assets) change
- When the container starts, it starts the daemon and hrana and mounts

## Tests

```sh
task test:vet            # go vet
task test:unit           # unit, crash injection, and restore tests (no containers needed)
task test:fuse           # FUSE integration tests (run in a Linux container)
task test:object-storage # integration tests with MinIO and fake-gcs-server
task test                # all of the above
```

## Checks against the development environment

Run against the daemon, hrana, and the mount started by `task run`.

### SQL front (hrana)

```sh
task check:hrana                  # checks INSERT, SELECT, UPDATE, DELETE and the refused statements
STORAGE=fake-gcs task check:hrana # when started with task run:fakegcs
```

- `check:hrana` writes `hrana-<date-time>.txt` at the root and logically deletes it at the end
- A failed check exits with code 1

### Load test

Runs reads and writes concurrently.

```sh
task check:load                                                 # through gRPC. Defaults (4 writers, 16 readers, 100 files, 64 KiB, 30 seconds)
task check:load -- -duration 1m -size 1048576                   # options follow --
task check:load:mount                                           # through the mount
task check:load:stress                                          # past the limits (doubles the writers from 4 up to 1024, holds 512 writes open, 10 seconds per stage)
task check:load:stress -- -mode mount -mountpoint /mnt/yaagwadb # past the limits through the mount
STORAGE=fake-gcs task check:load                                # if started with task run:fakegcs
```

- Creates the directory `load-<date and time>` and writes the files in it before measuring
- Writers overwrite existing files at random; readers read at random
- Each file data read is checked: the SHA-256 at the start must match the rest. A mismatch is an error
- For each stage, shows the count, errors, ops/s, MiB/s, latency (p50, p90, p99, max), and the count of each kind of error
- Afterwards the files and the directory are removed (logical deletion). `-keep` keeps them

#### Testing past the limits

- `-ramp-max`: doubles the writers from `-writers` every stage and stops at the first stage with errors
- `-hold`: number of writes started and left unfinished. They are held open during the run and finished afterwards
  - gRPC: only the target is sent. The daemon keeps a temporary file open and waits. Finishing cancels the stream (no file is registered)
  - Mount: the file is created, one byte is written, and it is kept open. Finishing closes it (the file is registered)
- Verification afterwards (always runs)
  - The daemon responds (Status)
  - The directory has no unexpected entries and no missing files
  - Every file reads back and matches its checksum
  - A new write and read work
  - If verification fails, the exit code is 1

Lowering the daemon's limit reaches it with less load.

```sh
task run:limited            # start with the daemon's open file limit at 256 (MinIO)
NOFILE=128 task run:limited # specify the limit
task check:load:stress      # run in another terminal
```

- Each write and read in progress uses one of the open files
- The thread limit cannot be lowered, because the container runs as root

#### Options

| Option | Default | Description |
| --- | --- | --- |
| `-c` | `/etc/yaagwadb/config.toml` | Configuration file to read `socket` from |
| `-socket` | (value in the configuration file) | Daemon socket. If given, `-c` is ignored. Also used for setup and verification through the mount |
| `-mode` | `grpc` | `grpc` or `mount` |
| `-mountpoint` | | Mount point (required with `-mode mount`) |
| `-writers` | `4` | Number of concurrent writers (the first stage with `-ramp-max`) |
| `-ramp-max` | `0` | Upper bound when doubling the writers. `0` runs a single stage |
| `-readers` | `16` | Number of concurrent readers |
| `-files` | `100` | Number of files |
| `-size` | `65536` | Bytes per file |
| `-duration` | `30s` | How long each stage runs |
| `-hold` | `0` | Number of writes held open |
| `-keep` | `false` | Keep the written files |

Outside the container, run `go run ./load -socket <socket path>`.

#### Results

- 2026-11-15
- `task check:load:mount` (MinIO, 4 writers, 16 readers, 64 KiB, 30 seconds)

| Files | Operation | Ops | Errors | ops/s | MiB/s | p50 | p90 | p99 | max |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1000 | Write | 910 | 0 | 30.3 | 1.89 | 99.4ms | 246.5ms | 454.8ms | 765.3ms |
| 1000 | Read | 85398 | 0 | 2839.8 | 177.49 | 5.2ms | 8.8ms | 13.3ms | 41.6ms |
| 10000 | Write | 979 | 0 | 32.5 | 2.03 | 95.8ms | 226.3ms | 407.1ms | 507.8ms |
| 10000 | Read | 78663 | 0 | 2612.0 | 163.25 | 5.6ms | 9.5ms | 14.6ms | 36.6ms |

- All verifications passed

### Benchmark

Writes and reads one file at a time and shows the difference from the plain file system.

```sh
task check:bench                                   # defaults (4 KiB, 64 KiB, 1 MiB, 16 MiB, 30 times each)
task check:bench -- -count 100 -sizes 4096,1048576 # options follow --
STORAGE=fake-gcs task check:bench                  # if started with task run:fakegcs
```

- Targets compared
  - `plain`: the plain file system, with fsync after each write (the baseline, because the daemon also fsyncs each write)
  - `plain-nosync`: the plain file system without fsync
  - `grpc`: through gRPC (Write for writes, Lookup and Read for reads)
  - `mount`: through the mount (only when `-mountpoint` is given)
- For each size and operation, shows latency (p50, p99, max), MiB/s, and the ratio to `plain` (p50 to p50)
- Runs one operation at a time, not in parallel. Use the load test for performance under load
- Reads of `plain` may be served from the page cache
- In `task check:bench`, the plain file system is the container's `/tmp`
- Afterwards the written files are removed. `-keep` keeps them

#### Options

| Option | Default | Description |
| --- | --- | --- |
| `-c` | `/etc/yaagwadb/config.toml` | Configuration file to read `socket` from |
| `-socket` | (value in the configuration file) | Daemon socket. If given, `-c` is ignored |
| `-mountpoint` | | Mount point. Empty skips the mount |
| `-plain` | the OS temporary directory | Directory on the plain file system to compare with |
| `-sizes` | `4096,65536,1048576,16777216` | File sizes in bytes, separated by commas |
| `-count` | `30` | Operations per size, target and operation |
| `-keep` | `false` | Keep the written files |

Outside the container, run `go run ./bench -socket <socket path> -mountpoint <mount point>`.

## Operating the development environment

### Working with files

The mount point (`/mnt/yaagwadb`) is located inside the container.
`docker cp` does not go through the mount, so use the following tasks.

```sh
task dev:put -- <local file> # write into the mount point under the same name
task dev:list                # list of files in the mount point (including dot files)
task dev:delete -- <name>    # delete from the mount point (a local path is also accepted; only its name is used)
task dev:shell               # start a shell at the mount point
```

Example of running a command such as `status`:

```sh
docker exec yaagwadb-develop /src/build/linux/yaagwadb -c /test/develop/configs/develop-minio.toml status
```

### Running SQL

Runs one statement through the hrana started by `task run` and prints the response.

```sh
task dev:sql -- 'SELECT path, size FROM objects' # runs one statement and prints the response
```

### Checking the object storage contents

```sh
task dev:list:minio   # objects in the MinIO bucket
task dev:list:fakegcs # objects in the fake-gcs-server bucket
```

The development bucket is `yaagwadb-develop`. The MinIO web console is at <http://localhost:9001> (user `yaagwadb`, password `yaagwadb-secret`).
