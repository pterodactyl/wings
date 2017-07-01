Vagrant.configure("2") do |cfg|
    cfg.vm.box = "ubuntu/xenial64"

    cfg.vm.synced_folder "./", "/home/ubuntu/go/src/github.com/Pterodactyl/wings"

    cfg.vm.provision :shell, path: ".dev/vagrant/provision.sh"

    cfg.vm.network :private_network, ip: "192.168.50.4"
end
