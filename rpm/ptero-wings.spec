Name:       ptero-wings
Version:    1.4.7
Release:    1%{?dist}
Summary:    The server control plane for Pterodactyl Panel. Written from the ground-up with security, speed, and stability in mind.
BuildArch:  x86_64
License:    GPLv3+
URL:        https://github.com/pterodactyl/wings
Source0:    https://github.com/pterodactyl/wings/releases/download/v%{version}/wings_linux_amd64


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
#nothing required

%build
#nothing required

%install
mkdir -p %{buildroot}%{_bindir}
cp %{_sourcedir}/wings_linux_amd64 %{buildroot}%{_bindir}/wings

%files
%{_bindir}/wings

%changelog
* Wed Aug 25 2021 Capitol Hosting Solutions Systems Engineering <syseng@chs.gg> - 1.4.7-1
- Spec File by Capitol Hosting Solutions, Upstream by Pterodactyl
- Rebased for https://github.com/pterodactyl/wings/releases/tag/v1.4.7
- SFTP access is now properly denied if a server is suspended.
- Correctly uses start_on_completion and crash_detection_enabled for servers.

