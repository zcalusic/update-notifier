// Copyright © 2016 Zlatko Čalušić
//
// Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.

// Update notifier displays notification and an icon in the panel tray area when Debian package updates are
// available. You can hover the mouse over the icon to check how many updates are available. It's especially suitable
// for Debian testing/unstable users, because it checks for updates very often. Developed and tested on Xfce desktop
// environment.
package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"time"

	"github.com/mattn/go-gtk/glib"
	"github.com/mattn/go-gtk/gtk"
	notify "github.com/mqu/go-notify"
)

var (
	aptRunning   bool
	aptLastCheck time.Time
	updLastCheck time.Time
	updAvailable int
	updTooltip   int
	updNotified  int
)

func showIcon(icon *gtk.StatusIcon) {
	if !icon.GetVisible() {
		icon.SetVisible(true)
	}
}

func hideIcon(icon *gtk.StatusIcon) {
	if icon.GetVisible() {
		icon.SetVisible(false)
	}
}

func showNotification(text string) {
	notify.Init("update-notifier")
	defer notify.UnInit()

	if notification := notify.NotificationNew("Software updates available", text,
		"/usr/share/icons/gnome/48x48/status/software-update-available.png"); notification != nil {
		_ = notify.NotificationShow(notification) // ignore errors
	}
}

func userNotify(icon *gtk.StatusIcon) {
	var word1, word2 string

	if updAvailable == 1 {
		word1 = "is"
		word2 = "update"
	} else {
		word1 = "are"
		word2 = "updates"
	}

	if updAvailable != updTooltip {
		icon.SetTooltipMarkup(fmt.Sprintf("%d %s available", updAvailable, word2))
		updTooltip = updAvailable
	}

	showIcon(icon)

	if updAvailable != updNotified {
		showNotification(fmt.Sprintf("There %s %d %s ready to install.", word1, updAvailable, word2))
		updNotified = updAvailable
	}
}

// Check if package manager is running (every 5 seconds).
func isAptRunning() bool {
	if time.Since(aptLastCheck) >= 5*time.Second {
		aptRunning = false

		dir, err := os.ReadDir("/proc")
		if err != nil {
			log.Fatal(err)
		}

		reApt := regexp.MustCompile(`(?m:^(apt-get|dselect|aptitude)$)`)
		reProcess := regexp.MustCompile(`^\d+$`)

		for _, v := range dir {
			if !v.IsDir() {
				continue
			}

			if !reProcess.MatchString(v.Name()) {
				continue
			}

			comm, err := os.ReadFile(path.Join("/proc", v.Name(), "comm"))
			if err != nil {
				continue
			}

			if reApt.Match(comm) {
				aptRunning = true
				updLastCheck = time.Time{}
				break
			}
		}

		aptLastCheck = time.Now()
	}

	return aptRunning
}

// Check if updates are available (every 5 minutes).
func updatesAvailable() int {
	if time.Since(updLastCheck) >= 5*time.Minute {
		var out bytes.Buffer

		cmd := exec.Command("/usr/bin/apt-get", "-s", "dist-upgrade")
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {

			if sl := regexp.MustCompile(`(?m:^(\d+) upgraded, (\d+) newly installed)`).FindStringSubmatch(out.String()); len(sl) == 3 {
				if upgraded, err := strconv.Atoi(sl[1]); err == nil {
					if newInstall, err := strconv.Atoi(sl[2]); err == nil {
						updAvailable = upgraded + newInstall
					}
				}
			}
		}

		updLastCheck = time.Now()
	}

	return updAvailable
}

func main() {
	// https://github.com/mattn/go-gtk/issues/251
	gtk.Init(nil)
	glib.SetApplicationName("update-notifier")

	icon := gtk.NewStatusIconFromFile("/usr/share/icons/gnome/24x24/status/software-update-available.png")
	icon.SetTitle("update-notifier")
	icon.SetVisible(false)

	// Don't fight with system notifications during login.
	time.Sleep(15 * time.Second)

	for {
		if !isAptRunning() && updatesAvailable() > 0 {
			userNotify(icon)
		} else {
			hideIcon(icon)
		}

		for gtk.EventsPending() {
			gtk.MainIteration()
		}

		time.Sleep(time.Second / 10)
	}
}
