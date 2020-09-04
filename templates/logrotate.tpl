{{.LogDirectory}}/wings.log {
    size 10M
    compress
    delaycompress
    dateext
    maxage 7
    missingok
    notifempty
    create 0640 {{.User.Uid}} {{.User.Gid}}
    postrotate
        killall -SIGHUP wings
    endscript
}