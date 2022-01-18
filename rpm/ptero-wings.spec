Name:       ptero-wings
Version:    1.5.3
Release:    1%{?dist}
Summary:    The server control plane for Pterodactyl Panel. Written from the ground-up with security, speed, and stability in mind.
BuildArch:  x86_64
License:    MIT
URL:        https://github.com/pterodactyl/wings
Source0:    https://github.com/pterodactyl/wings/releases/download/v%{version}/wings_linux_amd64

%if 0%{?rhel} && 0%{?rhel} <= 8
BuildRequires:  systemd
%else
BuildRequires:  systemd-rpm-macros
%endif


%description
Wings is Pterodactyl's server control plane, built for the rapidly
changing gaming industry and designed to be highly performant and
secure. Wings provides an HTTP API allowing you to interface directly
with running server instances, fetch server logs, generate backups,
and control all aspects of the server lifecycle.

In addition, Wings ships with a built-in SFTP server allowing your
system to remain free of Pterodactyl specific dependencies, and
allowing users to authenticate with the same credentials they would
normally use to access the Panel.

%prep

%build
#nothing required

%install
mkdir -p %{buildroot}%{_bindir}
mkdir -p %{buildroot}%{_unitdir}
cp %{_sourcedir}/wings_linux_amd64 %{buildroot}%{_bindir}/wings

cat > %{buildroot}%{_unitdir}/wings.service << EOF
[Unit]
Description=Pterodactyl Wings Daemon
After=docker.service
Requires=docker.service
PartOf=docker.service
StartLimitIntervalSec=600

[Service]
WorkingDirectory=/etc/pterodactyl
ExecStart=/usr/bin/wings
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
StartLimitInterval=180
StartLimitBurst=30
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

%files
%attr(0755, root, root) %{_prefix}/bin/wings
%attr(0644, root, root) %{_unitdir}/wings.service

%post

# Reload systemd
systemctl daemon-reload

# Create the required directory structure
mkdir -p /etc/pterodactyl
mkdir -p /var/lib/pterodactyl/{archives,backups,volumes}
mkdir -p /var/log/pterodactyl/install

%preun

systemctl is-active %{name} >/dev/null 2>&1
if [ $? -eq 0 ]; then
    systemctl stop %{name}
fi

systemctl is-enabled %{name} >/dev/null 2>&1
if [ $? -eq 0 ]; then
    systemctl disable %{name}
fi

%postun
rm -rf /var/log/pterodactyl

%verifyscript

wings --version

%changelog
* Wed Oct 27 2021 Capitol Hosting Solutions Systems Engineering <syseng@chs.gg> - 1.5.3-1
- specfile by Capitol Hosting Solutions, Upstream by Pterodactyl
- Rebased for https://github.com/pterodactyl/wings/releases/tag/v1.5.3
- Fixes improper event registration and error handling during socket authentication that would cause the incorrect error message to be returned to the client, or no error in some scenarios. Event registration is now delayed until the socket is fully authenticated to ensure needless listeners are not registed.
- Fixes dollar signs always being evaluated as environment variables with no way to escape them. They can now be escaped as $$ which will transform into a single dollar sign.
- A websocket connection to a server will be closed by Wings if there is a send error encountered and the client will be left to handle reconnections, rather than simply logging the error and continuing to listen for new events.

* Sun Sep 12 2021 Capitol Hosting Solutions Systems Engineering <syseng@chs.gg> - 1.5.0-1
- specfile by Capitol Hosting Solutions, Upstream by Pterodactyl
- Rebased for https://github.com/pterodactyl/wings/releases/tag/v1.5.0
- Fixes a race condition when setting the application name in the console output for a server.
- Fixes a server being reinstalled causing the file_denylist parameter for an Egg to be ignored until Wings is restarted.
- Fixes YAML file parser not correctly setting boolean values.
- Fixes potential issue where the underlying websocket connection is closed but the parent request context is not yet canceled causing a write over a closed connection.
- Fixes race condition when closing all active websocket connections when a server is deleted.
- Fixes logic to determine if a server's context is closed out and send a websocket close message to connected clients. Previously this fired off whenever the request itself was closed, and not when the server context was closed.
- Exposes 8080 in the wings Dockerfile to better support reverse proxy tools.
- Releases are now built using Go 1.17 — the minimum version required to build Wings remains Go 1.16.
- Simplifed the logic powering server updates to only pull information from the Panel rather than trying to accept updated values. All parts of Wings needing the most up-to-date server details should call Server#Sync() to fetch the latest stored build information.
- Installer#New() no longer requires passing all of the server data as a byte slice, rather a new Installer#ServerDetails struct is exposed which can be passed and accepts a UUID and if the server should be started after the installer finishes.
- Removes complicated (and unused) logic during the server installation process that was a hold-over from legacy Wings architectures.
- Removes the PATCH /api/servers/:server endpoint — if you were previously using this API call it should be replaced with POST /api/servers/:server/sync.

* Wed Aug 25 2021 Capitol Hosting Solutions Systems Engineering <syseng@chs.gg> - 1.4.7-1
- specfile by Capitol Hosting Solutions, Upstream by Pterodactyl
- Rebased for https://github.com/pterodactyl/wings/releases/tag/v1.4.7
- SFTP access is now properly denied if a server is suspended.
- Correctly uses start_on_completion and crash_detection_enabled for servers.
