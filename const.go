package main

import "os"

const (
    Version = "0.0.1"

    // DefaultFilePerms are the file perms used for created files.
    DefaultFilePerms os.FileMode = 0644

    // DefaultFolderPerms are the file perms used for created folders.
    DefaultFolderPerms os.FileMode = 0755

    // ServersPath is the path of the servers within the configured DataPath.
    ServersPath = "servers"

    // ServerConfigFile is the filename of the server config file.
    ServerConfigFile = "server.json"

    // ServerDataPath is the path of the data of a single server.
    ServerDataPath = "data"

    // DockerContainerPrefix is the prefix used for naming Docker containers.
    // It's also used to prefix the hostnames of the docker containers.
    DockerContainerPrefix = "ptdl-"

    // WSMaxMessages is the maximum number of messages that are sent in one transfer.
    WSMaxMessages = 10
)
