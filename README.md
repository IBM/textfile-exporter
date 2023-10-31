# textfile-exporter
A prometheus target for metrics from textual files.

Useful when you have prometheus metrics entries in one (or more) textual files and want them exported so that prometheus can scrape them.
A typical use case is when exporting results of cronjobs or other processes that just exit after executing their task and/or are not able to export metrics directly on a socket.

## Usage
The process is expected to run as a daemon; a systemd unit file is included as an example.

The specified directory will be continuously monitored for `*.prom` files and all metrics included in them will be gathered. When a HTTP GET request is received on the specified tcp port (and `/metrics` endpoint), the metrics are emitted for grabbing.

Example of a metrics prom file (having many files named `*.prom` would be a more realistic case):

```
$ cat /myproject/metrics/backup-job.prom  
```
```
# HELP backup_duration_seconds Duration of backup process in seconds.
# TYPE backup_duration_seconds gauge
backup_duration_seconds{host="server1"} 138 1698680700000
# HELP backup_copied_bytes Amount of data copied in bytes.
# TYPE backup_copied_bytes gauge
backup_copied_bytes{host="server1"} 37543832 1698680700000
```

**Timestamps are explicitly supported**, and usually important given that data are produced in one moment and scraped in a successive one.
In particular, even if a file is present for a long time and many scraping operations happen, the timestamp will still specify the correct production time of the metrics (e.g. when the backup has been executed). The exporter can also be configured to perform a cleanup for really old prom files (see options `-o` and `-x`).

Example of running the exporter:  
```
$ textfile-exporter -p /myproject/metrics  
```

Example of scraping the metrics:  
```
$ curl http://localhost:9014/metrics  
```
```
# HELP backup_copied_bytes Amount of data copied in bytes.
# TYPE backup_copied_bytes gauge
backup_copied_bytes{host="server1"} 3.7543832e+07 1698680700000
# HELP backup_duration_seconds Duration of backup process in seconds.
# TYPE backup_duration_seconds gauge
backup_duration_seconds{host="server1"} 138 1698680700000
```

## Configuration
Some configuration options are available:
```  
  -i duration
        scan interval (default 30s)
  -l int
        listen port (default 9014)
  -m duration
        max age of in memory metrics (default 25h0m0s)
  -o duration
        min age of files considered old (default 6h0m0s)
  -p string
        path for prom file or dir of *.prom files (default ".")
  -x string
        external command executed on old files (default "ls -l {}")
```
to configure
- `-i` the directory scan interval
- `-l` the port to listen on
- `-m` the garbage collection expiration for metrics loaded in the memory of the process
- `-o` after how much time a (optional) external cleanup script gets called 
- `-p` the absolute path where `*.prom` files have to be searched 
- `-x` the (optional) external command executed on old prom files 

## Advanced features
### live debug
If you want to enable debug mode on a running process you can just create a file called `debug_tfe` in the monitored path (`touch /myproject/metrics/debug_tfe`). The presence of this file will trigger debug output until the file gets removed; the file will be ignored if it is older than two hours so to automatically interrupt a forgotten debug session that may cause large debug output in logs. This feature is useful when stopping and restarting the service may be inconvenient (e.g. running inside a container setup).
### liveness endpoint
An HTTP GET to the `/alive` endpoint can be used to check if the daemon is running and operating correctly. This may be useful in a container setup to trigger remediations (automatic restart) without hitting the `/metrics` endpoint that would produce output we do not actually need to get.
