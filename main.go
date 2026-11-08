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

const (
	aptGet  = "/usr/bin/apt-get"
	procDir = "/proc"
)

var (
	reApt     = regexp.MustCompile(`(?m:^(apt-get|dselect|aptitude)$)`)
	reProcess = regexp.MustCompile(`^\d+$`)
	reSummary = regexp.MustCompile(`(?m:^(\d+) upgraded, (\d+) newly installed)`)
)

// tray is the part of the desktop that the notifier talks to.
type tray interface {
	show()
	hide()
	setTooltip(text string)
	notify(text string)
}

// gtkTray is a tray backed by a GTK status icon and libnotify.
type gtkTray struct {
	icon *gtk.StatusIcon
}

func (t gtkTray) show() {
	if !t.icon.GetVisible() {
		t.icon.SetVisible(true)
	}
}

func (t gtkTray) hide() {
	if t.icon.GetVisible() {
		t.icon.SetVisible(false)
	}
}

func (t gtkTray) setTooltip(text string) {
	t.icon.SetTooltipMarkup(text)
}

func (gtkTray) notify(text string) {
	notify.Init("update-notifier")
	defer notify.UnInit()

	if notification := notify.NotificationNew("Software updates available", text,
		"/usr/share/icons/gnome/48x48/status/software-update-available.png"); notification != nil {
		_ = notify.NotificationShow(notification) // ignore errors
	}
}

// notifier holds the state of the update checks.
type notifier struct {
	tray     tray
	procDir  string
	simulate func() (string, error) // output of a simulated upgrade
	now      func() time.Time

	aptRunning   bool
	aptLastCheck time.Time
	updLastCheck time.Time
	updAvailable int
	updTooltip   int
	updNotified  int
}

func (n *notifier) userNotify() {
	var word1, word2 string

	if n.updAvailable == 1 {
		word1 = "is"
		word2 = "update"
	} else {
		word1 = "are"
		word2 = "updates"
	}

	if n.updAvailable != n.updTooltip {
		n.tray.setTooltip(fmt.Sprintf("%d %s available", n.updAvailable, word2))
		n.updTooltip = n.updAvailable
	}

	n.tray.show()

	if n.updAvailable != n.updNotified {
		n.tray.notify(fmt.Sprintf("There %s %d %s ready to install.", word1, n.updAvailable, word2))
		n.updNotified = n.updAvailable
	}
}

// Check if package manager is running (every 5 seconds).
func (n *notifier) isAptRunning() bool {
	if n.now().Sub(n.aptLastCheck) >= 5*time.Second {
		n.aptRunning = false

		dir, err := os.ReadDir(n.procDir)
		if err != nil {
			log.Fatal(err)
		}

		for _, v := range dir {
			if !v.IsDir() {
				continue
			}

			if !reProcess.MatchString(v.Name()) {
				continue
			}

			comm, err := os.ReadFile(path.Join(n.procDir, v.Name(), "comm"))
			if err != nil {
				continue
			}

			if reApt.Match(comm) {
				n.aptRunning = true
				n.updLastCheck = time.Time{}
				break
			}
		}

		n.aptLastCheck = n.now()
	}

	return n.aptRunning
}

// parseSummary returns the number of updates from the output of a simulated upgrade.
func parseSummary(out string) (int, bool) {
	sl := reSummary.FindStringSubmatch(out)
	if len(sl) != 3 {
		return 0, false
	}

	upgraded, err := strconv.Atoi(sl[1])
	if err != nil {
		return 0, false
	}

	newInstall, err := strconv.Atoi(sl[2])
	if err != nil {
		return 0, false
	}

	return upgraded + newInstall, true
}

// Check if updates are available (every 5 minutes).
func (n *notifier) updatesAvailable() int {
	if n.now().Sub(n.updLastCheck) >= 5*time.Minute {
		if out, err := n.simulate(); err == nil {
			if count, ok := parseSummary(out); ok {
				n.updAvailable = count
			}
		}

		n.updLastCheck = n.now()
	}

	return n.updAvailable
}

// tick is one pass of the main loop.
func (n *notifier) tick() {
	if !n.isAptRunning() && n.updatesAvailable() > 0 {
		n.userNotify()
	} else {
		n.tray.hide()
	}
}

// aptSimulate returns the output of a simulated upgrade, run with the given apt-get binary.
func aptSimulate(binary string) (string, error) {
	var out bytes.Buffer

	cmd := exec.Command(binary, "-s", "dist-upgrade")
	cmd.Env = append(os.Environ(), "LC_ALL=C") // the summary line is parsed, so it must not be translated
	cmd.Stdout = &out
	err := cmd.Run()

	return out.String(), err
}

func main() {
	// https://github.com/mattn/go-gtk/issues/251
	gtk.Init(nil)
	glib.SetApplicationName("update-notifier")

	icon := gtk.NewStatusIconFromFile("/usr/share/icons/gnome/24x24/status/software-update-available.png")
	icon.SetTitle("update-notifier")
	icon.SetVisible(false)

	n := &notifier{
		tray:     gtkTray{icon: icon},
		procDir:  procDir,
		simulate: func() (string, error) { return aptSimulate(aptGet) },
		now:      time.Now,
	}

	// Don't fight with system notifications during login.
	time.Sleep(15 * time.Second)

	for {
		n.tick()

		for gtk.EventsPending() {
			gtk.MainIteration()
		}

		time.Sleep(time.Second / 10)
	}
}
