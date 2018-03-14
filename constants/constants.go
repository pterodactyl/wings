package constants

import "os"

// Version is the current wings version.
const Version = "0.0.1-alpha"

/* ---------- PATHS ---------- */

// DefaultFilePerms are the file perms used for created files.
const DefaultFilePerms os.FileMode = 0644

// DefaultFolderPerms are the file perms used for created folders.
const DefaultFolderPerms os.FileMode = 0744

// ServersPath is the path of the servers within the configured DataPath.
const ServersPath = "servers"

// ServerConfigFile is the filename of the server config file.
const ServerConfigFile = "server.json"

// ServerDataPath is the path of the data of a single server.
const ServerDataPath = "data"

/* ---------- MISC ---------- */

// JSONIndent is the indent to use with the json.MarshalIndent() function.
const JSONIndent = "  "

// DockerContainerPrefix is the prefix used for naming Docker containers.
// It's also used to prefix the hostnames of the docker containers.
const DockerContainerPrefix = "ptdl-"

// WSMaxMessages is the maximum number of messages that are sent in one transfer.
const WSMaxMessages = 10
