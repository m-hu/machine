package provision

import (
	"fmt"

	"github.com/docker/machine/libmachine/auth"
	"github.com/docker/machine/libmachine/drivers"
	"github.com/docker/machine/libmachine/engine"
	"github.com/docker/machine/libmachine/log"
	"github.com/docker/machine/libmachine/provision/pkgaction"
	"github.com/docker/machine/libmachine/provision/serviceaction"
	"github.com/docker/machine/libmachine/swarm"
)

func init() {
	Register("AlpineLinux", &RegisteredProvisioner{
		New: NewAlpineProvisioner,
	})
}

func NewAlpineProvisioner(d drivers.Driver) Provisioner {
	return &AlpineProvisioner{
		GenericProvisioner{
			SSHCommander:      GenericSSHCommander{Driver: d},
			DockerOptionsDir:  "/etc/docker",
			DaemonOptionsFile: "/etc/conf.d/docker",
			OsReleaseID:       "alpine",
			Packages: []string{
				"docker",
			},
			Driver: d,
		},
	}
}

type AlpineProvisioner struct {
	GenericProvisioner
}

func (provisioner *AlpineProvisioner) String() string {
	return "alpine"
}

func (provisioner *AlpineProvisioner) Service(name string, action serviceaction.ServiceAction) error {
	command := fmt.Sprintf("sudo rc-service %s %s", name, action.String())

	if _, err := provisioner.SSHCommand(command); err != nil {
		return err
	}

	return nil
}

func (provisioner *AlpineProvisioner) Package(name string, action pkgaction.PackageAction) error {
	if name == "docker" && action == pkgaction.Upgrade {
		return provisioner.upgrade()
	}
	var command string
	switch action {
	case pkgaction.Install:
		command = fmt.Sprintf("sudo rc-update add %s boot", name)
	case pkgaction.Remove:
		command = fmt.Sprintf("sudo rc-update del %s boot", name)
	}

	if _, err := provisioner.SSHCommand(command); err != nil {
		return err
	}

	return nil
}

func (provisioner *AlpineProvisioner) Provision(swarmOptions swarm.Options, authOptions auth.Options, engineOptions engine.Options) error {
	log.Debugf("Running RancherOS provisioner on %s", provisioner.Driver.GetMachineName())

	provisioner.SwarmOptions = swarmOptions
	provisioner.AuthOptions = authOptions
	provisioner.EngineOptions = engineOptions
	swarmOptions.Env = engineOptions.Env

	if provisioner.EngineOptions.StorageDriver == "" {
		provisioner.EngineOptions.StorageDriver = "overlay"
	} else if provisioner.EngineOptions.StorageDriver != "overlay" {
		return fmt.Errorf("Unsupported storage driver: %s", provisioner.EngineOptions.StorageDriver)
	}

	log.Debugf("Setting hostname %s", provisioner.Driver.GetMachineName())
	if err := provisioner.SetHostname(provisioner.Driver.GetMachineName()); err != nil {
		return err
	}

	for _, pkg := range provisioner.Packages {
		log.Debugf("Installing package %s", pkg)
		if err := provisioner.Package(pkg, pkgaction.Install); err != nil {
			return err
		}
	}

	if engineOptions.InstallURL == drivers.DefaultEngineInstallURL {
		log.Debugf("Skipping docker engine default: %s", engineOptions.InstallURL)
	} else {
		log.Debugf("Selecting docker engine: %s", engineOptions.InstallURL)
		if err := selectDocker(provisioner, engineOptions.InstallURL); err != nil {
			return err
		}
	}

	if err := makeDockerOptionsDir(provisioner); err != nil {
		return err
	}

	log.Debugf("Preparing certificates")
	provisioner.AuthOptions = setRemoteAuthOptions(provisioner)

	log.Debugf("Setting up certificates")
	if err := ConfigureAuth(provisioner); err != nil {
		return err
	}

	log.Debugf("Configuring swarm")
	err := configureSwarm(provisioner, swarmOptions, provisioner.AuthOptions)
	return err
}

func (provisioner *AlpineProvisioner) SetHostname(hostname string) error {
	// /etc/hosts is bind mounted from Docker, this is hack to that the generic provisioner doesn't try to mv /etc/hosts
	if _, err := provisioner.SSHCommand("sed /127.0.1.1/d /etc/hosts > /tmp/hosts && cat /tmp/hosts | sudo tee /etc/hosts"); err != nil {
		return err
	}

	if err := provisioner.GenericProvisioner.SetHostname(hostname); err != nil {
		return err
	}

	if _, err := provisioner.SSHCommand(fmt.Sprintf(hostnameTmpl, hostname)); err != nil {
		return err
	}

	return nil
}

func (provisioner *AlpineProvisioner) upgrade() error {
	switch provisioner.Driver.DriverName() {
	default:
		log.Infof("Running upgrade")
		if _, err := provisioner.SSHCommand("sudo apk upgrade"); err != nil {
			return err
		}

		log.Infof("Upgrade succeeded, rebooting")
		// ignore errors here because the SSH connection will close
		provisioner.SSHCommand("sudo reboot")

		return nil
	}
}
