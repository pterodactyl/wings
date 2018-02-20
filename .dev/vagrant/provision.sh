#!/bin/bash

echo "Provisioning development environment for Pterodactyl go daemon."
cp /home/ubuntu/go/github.com/pterodactyl/wings.go/.dev/vagrant/motd.txt /etc/motd

chown -R ubuntu:ubuntu /home/ubuntu/go
chown -R ubuntu:ubuntu /srv

echo "Update apt repositories"
sudo add-apt-repository ppa:longsleep/golang-backports
apt-get update > /dev/null

echo "Install docker"
curl -sSL https://get.docker.com/ | sh
systemctl enable docker
usermod -aG docker ubuntu

echo "Install go"
apt-get install -y golang-go
echo "export GOPATH=/home/ubuntu/go" >> /home/ubuntu/.profile
export GOPATH=/go
echo 'export PATH=$PATH:$GOPATH/bin' >> /home/ubuntu/.profile

echo "Install go dep"
sudo -H -u ubuntu bash -c 'go get -u github.com/golang/dep/cmd/dep'

echo "Install delve for debugging"
sudo -H -u ubuntu bash -c 'go get -u github.com/derekparker/delve/cmd/dlv'

echo "Install additional dependencies"
apt-get -y install mercurial #tar unzip make gcc g++ python > /dev/null

echo "   ------------"
echo "Gopath is /home/ubuntu/go"
echo "The project is mounted to /home/ubuntu/go/src/github.com/pterodactyl/wings.go"
echo "Provisioning is completed."
