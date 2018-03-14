#!/bin/bash

echo "Provisioning development environment for Pterodactyl go daemon."
cp /home/vagrant/go/github.com/pterodactyl/wings.go/.dev/vagrant/motd.txt /etc/motd

chown -R vagrant:vagrant /home/vagrant/go
chown -R vagrant:vagrant /srv

echo "Update apt repositories"
sudo add-apt-repository ppa:longsleep/golang-backports
apt-get update > /dev/null

echo "Install docker"
curl -sSL https://get.docker.com/ | sh
systemctl enable docker
usermod -aG docker vagrant

echo "Install go"
apt-get install -y golang-go
echo "export GOPATH=/home/vagrant/go" >> /home/vagrant/.profile
export GOPATH=/go
echo 'export PATH=$PATH:$GOPATH/bin' >> /home/vagrant/.profile

echo "Install go dep"
sudo -H -u vagrant bash -c 'go get -u github.com/golang/dep/cmd/dep'

echo "Install delve for debugging"
sudo -H -u vagrant bash -c 'go get -u github.com/derekparker/delve/cmd/dlv'

echo "Install additional dependencies"
apt-get -y install mercurial #tar unzip make gcc g++ python > /dev/null

echo "Install ctop for fancy container monitoring"
wget https://github.com/bcicen/ctop/releases/download/v0.7.1/ctop-0.7.1-linux-amd64 -O /usr/local/bin/ctop
chmod +x /usr/local/bin/ctop

echo "   ------------"
echo "Gopath is /home/vagrant/go"
echo "The project is mounted to /home/vagrant/go/src/github.com/pterodactyl/wings"
echo "Provisioning is completed."
