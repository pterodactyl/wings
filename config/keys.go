package config

const (
	// Debug is a boolean value that enables debug mode
	Debug = "debug"

	// DataPath is a string containing the path where data should
	// be stored on the system
	DataPath = "data"

	// APIHost is a string containing the interface ip address
	// on what the api should listen on
	APIHost = "api.host"
	// APIPort is an integer containing the port the api should
	// listen on
	APIPort = "api.port"
	// SSLEnabled is a boolean that states whether ssl should be enabled or not
	SSLEnabled = "api.ssl.enabled"
	// SSLGenerateLetsencrypt is a boolean that enables automatic SSL certificate
	// creation with letsencrypt
	SSLGenerateLetsencrypt = "api.ssl.letsencrypt"
	// SSLCertificate is a string containing the location of
	// a ssl certificate to use
	SSLCertificate = "api.ssl.cert"
	// SSLKey is a string containing the location of the key
	// for the ssl certificate
	SSLKey = "api.ssl.key"
	// UploadsMaximumSize is an integer that sets the maximum size for
	// file uploads through the api in Kilobytes
	UploadsMaximumSize = "api.uploads.maximumSize"

	// DockerSocket is a string containing the path to the docker socket
	DockerSocket = "docker.socket"
	// DockerAutoupdateImages is a boolean that enables automatic
	// docker image updates on every server install
	DockerAutoupdateImages = "docker.autoupdateImages"
	// DockerNetworkInterface is a string containing the network interface
	// to use for the wings docker network
	DockerNetworkInterface = "docker.networkInterface"
	// DockerTimezonePath is a string containing the path to the timezone
	// file that should be mounted into all containers
	DockerTimezonePath = "docker.timezonePath"

	// SftpHost is a string containing the interface ip address on
	// which the sftp server should be listening
	SftpHost = "sftp.host"
	// SftpPort is an integer containing the port the sftp servers hould
	// be listening on
	SftpPort = "sftp.port"

	// Remote is a string containing the url to the Pterodactyl panel
	// wings should send updates to
	Remote = "remote"

	// LogPath is a string containing the path where logfiles should be
	// stored
	LogPath = "log.path"
	// LogLevel is a string containing the log level
	LogLevel = "log.level"
	// LogDeleteAfterDays is an integer containing the amounts of days
	// logs should be stored. They will be deleted after. If set to 0
	// logs will be stored indefinitely.
	LogDeleteAfterDays = "log.deleteAfterDays"
	// AuthKey contains a key that will be replaced by something better
	AuthKey = "authKey"
)
