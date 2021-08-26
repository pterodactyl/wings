Name:       ptero-wings
Version:    1.4.7
Release:    1%{?dist}
Summary:    The server control plane for Pterodactyl Panel. Written from the ground-up with security, speed, and stability in mind.
BuildArch:  x86_64
License:    GPLv3+
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
LimitNOFILE=4096

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
* Wed Aug 25 2021 Capitol Hosting Solutions Systems Engineering <syseng@chs.gg> - 1.4.7-1
- specfile by Capitol Hosting Solutions, Upstream by Pterodactyl
- Rebased for https://github.com/pterodactyl/wings/releases/tag/v1.4.7
- SFTP access is now properly denied if a server is suspended.
- Correctly uses start_on_completion and crash_detection_enabled for servers.