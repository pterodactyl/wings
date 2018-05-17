Vagrant.configure("2") do |cfg|
    cfg.vm.box = "bento/ubuntu-16.04"

    cfg.vm.synced_folder "./", "/home/vagrant/go/src/github.com/pterodactyl/wings"

    cfg.vm.provision :shell, path: ".dev/vagrant/provision.sh"

    cfg.vm.network :private_network, ip: "192.168.50.4"
end
