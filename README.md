# Update notifier

[![Build Status](https://travis-ci.org/zcalusic/update-notifier.svg?branch=master)](https://travis-ci.org/zcalusic/update-notifier)
[![Go Report Card](https://goreportcard.com/badge/github.com/zcalusic/update-notifier)](https://goreportcard.com/report/github.com/zcalusic/update-notifier)
[![GoDoc](https://godoc.org/github.com/zcalusic/update-notifier?status.svg)](https://godoc.org/github.com/zcalusic/update-notifier)
[![License](https://img.shields.io/badge/license-MIT-a31f34.svg?maxAge=2592000)](https://github.com/zcalusic/update-notifier/blob/master/LICENSE)
[![Powered by](https://img.shields.io/badge/powered_by-Go-5272b4.svg?maxAge=2592000)](https://golang.org/)
[![Platform](https://img.shields.io/badge/platform-Linux-009bde.svg?maxAge=2592000)](https://www.linuxfoundation.org/)

Update notifier displays notification and an icon in the panel tray area when Debian package updates are available. You
can hover the mouse over the icon to check how many updates are available. It's especially suitable for Debian
testing/unstable users, because it checks for updates very often. Developed and tested on Xfce desktop environment.

![notification](https://cloud.githubusercontent.com/assets/12140851/17998625/ac821782-6b75-11e6-911a-dc0e9f2cffa0.png)
&nbsp;
![trayicon](https://cloud.githubusercontent.com/assets/12140851/17998626/ac84dfda-6b75-11e6-8ac7-c06486ff6a37.png)

## Motivation

Once upon a time, there was this neat little package in the Debian GNU/Linux repository called update-notifier. It
notified when package updates were available. Nowadays, the same named package in the repository is marked transitional,
and is depending on quite a large number of other application and library packages. But, I wanted just the icon! So, I
used that as an excuse to see how hard it would be to write something like that in Go. Not at all, it seems.

## Requirements

To compile:
- Go 1.6+
- libgtk2.0-dev
- libnotify-dev

To run:
- libgtk2.0-0
- libnotify4
- gnome-icon-theme (icons)
- apt-get (to check for updates)
- apt-daily.timer (or similar, to periodically update package lists in the background)

## Installation

Just use go get.

```
go get github.com/zcalusic/update-notifier
```

Here's a simple desktop file you can save as ```~/.config/autostart/update-notifier.desktop```, so that application is
automatically started when you login. Just adapt TryExec directive to point to the location of the update-notifier
executable on your system.

```ini
[Desktop Entry]
Encoding=UTF-8
Name=Update notifier
Comment=Notify when package updates are available
TryExec=/path/to/update-notifier
Exec=update-notifier
Terminal=false
Type=Application
Categories=
Hidden=false
```

## Contributors

Contributors are welcome, just open a new issue / pull request.

## License

```
The MIT License (MIT)

Copyright © 2016 Zlatko Čalušić

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
