# Changelog

## v1.0.0-beta.3
### Fixed
* Daemon will no longer crash if someone requests a websocket for a deleted server.
* Temporary directories are now created properly if missing during the server installation process.

### Added
* Added support for using Amazon S3 as a backup location for archives.

### Changed
* Memory overhead for containers is now 5/10/15% higher than the passed limit to account for JVM heap and prevent crashing.

## v1.0.0-alpha.2
### Added
* Ability to run an installation process for a server and notify the panel when completed.
* Output from the installation process is now emitted over the server console for administrative users to view.

### Fixed
* Fixed bugs caused when unmarshaling data into a struct with default values.
* Timezone is properly set in containers by using the `TZ=` environment variable rather than mounting timezone data into the filesystem.
* Server data directly is now properly created when running the install process.
* Time drift caused in the socket JWT is now handled in a better manner and less likely to unexpectedly error out.