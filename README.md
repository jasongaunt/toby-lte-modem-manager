
# Toby LTE Modem Manager

## Introduction

This small Go program is designed to bring up a U-Blox Toby LTE modem and keep it online as best it can.

It does this by initialising the modem into a known-good state, setting up a default PDP context, verifying that there's a valid connection and then assigning the IP to the specified interface.

After that, it will then check ping responses to the primary nameserver that came back from the PDP context. If there are more than 3 ping timeouts, it assumes the connection has dropped and restarts the modem again.

This service should be managed by one of the supplied init or systemd scripts, but you can call it from the command line if you wish using the following command-line options:

```
./toby-lte-modem-manager  usage:
  -a string
        APN (Access Point Name) for mobile provider
  -b uint
        Baud rate (default 115200)
  -d string
        serial port for modem (/dev/ttyUSB0, etc)
  -i string
        Network interface to assign IP to (ie. wwan0)
  -v    Verbose output
  -vv   Debug output (warning: noisy!)
```

**Warning**:
This application currently only support Linux (or any other OS that has `ifconfig`, `route` and `/etc/resolv.conf`).

It does *not* support Windows (yet) nor any other operating system.

## Compiling

### Pre-requisites
Before you begin, you will need a recent version of Go on your system (on my system, I built it with `go version go1.13.8 linux/amd64`)

You will also require a couple of third party libraries, you can install them with the following command;

```bash
go get -u github.com/jacobsa/go-serial/serial github.com/tatsushid/go-fastping
```

Although you can use this Go script simply with `go run toby-lte-modem-manager.go`, it's been written with the intent of being (cross) compiled into a static binary so it can be installed on other systems.

Compiling should occur on a suitable Linux system ie. Ubuntu or Debian.

To compile, run one of the following;

### Compiling for the current processor architecture
```bash
go build toby-lte-modem-manager.go
```

### Cross-compiling for ARM7 (Raspberry Pi 1 / 2 / Nano, Toradex / NXP iMX6 etc)
```bash
env GOOS=linux GOARCH=arm GOARM=7 go build toby-lte-modem-manager.go
```

### Cross-compiling for ARM8 (Raspberry Pi 3-onwards, Toradex / NXP iMX8 etc)
```bash
env GOOS=linux GOARCH=arm64 GOARM=8 go build toby-lte-modem-manager.go
```

### Cross-compiling for Mac
```bash
env GOOS=darwin GOARCH=amd64 go build toby-lte-modem-manager.go
```

The code should then build (usually in under 10 seconds, [YMMV](https://dictionary.cambridge.org/dictionary/english/ymmv)). 

## Installation

**Warning:** init.d scripts do not auto-restart if there is a failure, something this Go application may depend upon if the modem needs to be restarted after configuring the networking mode. Please consider using *systemd* where possible!

Depending on your target operating system, you will need to do *one* of the following;

### init.d

1. Copy `toby-lte-modem-manager` to your `/usr/bin/` folder (or another suitable place, remember to update this in the init script below)
2. Copy `toby-lte-modem-manager.init` to your init.d folder (most likely `/etc/init.d/`)
3. Edit that file and change the launch parameters (especially *CellProviderAPN*) on the following line to suit your setup;
```SCRIPT="/usr/bin/toby-lte-modem-manager -d /dev/ttyUSB0 -a CellProviderAPN -i wwan0 -v"```
4. Start the service with `service toby-lte-modem-manager start`
5. Tell the init.d system to run the script on boot with either of the following;
	* For Ubuntu and variants: `update-rc.d toby-lte-modem-manager defaults`
	* For Redhat and variants: `chkconfig toby-lte-modem-manager on`

### systemd

1. Copy `toby-lte-modem-manager` to your `/usr/bin/` folder (or another suitable place, remember to update this in the init script below)
2. Copy `toby-lte-modem-manager.service` to your systemd *system* folder (most likely `/etc/systemd/system/`)
3. Edit that file and change the launch parameters (especially *CellProviderAPN*) on the following line to suit your setup;
```ExecStart=/usr/bin/toby-lte-modem-manager -d /dev/ttyUSB0 -a CellProviderAPN -i wwan0 -v```
4. Start the service with `systemctl daemon-reload` and then `systemctl start toby-lte-modem-manager`
5. Tell systemd to run the script on boot with `systemctl enable toby-lte-modem-manager`

## Checking the logs

For init.d you can find the logs in your system log ie. `/var/log/syslog` or `/var/log/messages`

For systemd users you can get the logs by running `journalctl -fu toby-lte-modem-manager`

## Contributing

Pull requests are welcome. For major changes, please open an issue first to discuss what you would like to change.

## Contributors
* Jason Gaunt - Initial Application

## Copyrights
* The u-blox and TOBY names are Copyright © [u-blox AG](https://www.u-blox.com/)

## License
[MIT](https://choosealicense.com/licenses/mit/)

