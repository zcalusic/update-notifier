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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mattn/go-gtk/glib"
	"github.com/mattn/go-gtk/gtk"
	notify "github.com/mqu/go-notify"
)

const (
	aptGet     = "/usr/bin/apt-get"
	procLocks  = "/proc/locks"
	dpkgStatus = "/var/lib/dpkg/status"

	usage = `Usage: update-notifier [-h] [-version]

Shows an icon in the panel tray area and a notification when Debian package updates are available.

  -h        show this help
  -version  show the version
`
)

var (
	// A lock held on any of these means that a package manager is at work. The files are readable only by root, but
	// the kernel reports their locks to everyone in /proc/locks.
	packageLocks = []string{
		"/var/lib/dpkg/lock-frontend",
		"/var/lib/dpkg/lock",
		"/var/lib/apt/lists/lock",
	}

	// The inode of a locked file, from a line like "1: POSIX ADVISORY WRITE 1234 fe:01:131 0 EOF".
	reLock    = regexp.MustCompile(`[0-9a-f]+:[0-9a-f]+:(\d+) `)
	reSummary = regexp.MustCompile(`(?m:^(\d+) upgraded, (\d+) newly installed)`)
)

func init() {
	runtime.LockOSThread() // all GTK calls must come from the thread that initialized it
}

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
	tray       tray
	locksFile  string                 // /proc/locks
	lockFiles  []string               // files that a package manager locks
	statusFile string                 // rewritten by dpkg whenever packages change
	simulate   func() (string, error) // output of a simulated upgrade
	now        func() time.Time
	logger     *log.Logger

	aptRunning   bool
	aptLastCheck time.Time
	statusTime   time.Time // modification time of statusFile, when last seen
	locksFailed  bool      // reading locksFile failed, and that has been logged
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

// lockHeld reports whether any of the lock files is locked. Only inodes are compared, because on some filesystems (btrfs)
// the device numbers in /proc/locks differ from the ones that stat returns.
func (n *notifier) lockHeld() bool {
	locks, err := os.ReadFile(n.locksFile)
	if err != nil {
		if !n.locksFailed {
			n.logger.Printf("can't check for a running package manager: %v", err)
			n.locksFailed = true
		}

		return false
	}

	n.locksFailed = false

	held := make(map[string]bool)
	for _, m := range reLock.FindAllSubmatch(locks, -1) {
		held[string(m[1])] = true
	}

	for _, file := range n.lockFiles {
		var st syscall.Stat_t
		if err := syscall.Stat(file, &st); err != nil {
			continue // not created yet
		}

		if held[strconv.FormatUint(st.Ino, 10)] {
			return true
		}
	}

	return false
}

// packagesChanged reports whether the status file changed since the last call. A package manager can come and go between
// two looks at its locks, but it can't change the packages without leaving this behind.
func (n *notifier) packagesChanged() bool {
	info, err := os.Stat(n.statusFile)
	if err != nil {
		return false
	}

	changed := !info.ModTime().Equal(n.statusTime)
	n.statusTime = info.ModTime()

	return changed
}

// Check if package manager is running (every second).
func (n *notifier) isAptRunning() bool {
	if n.now().Sub(n.aptLastCheck) >= time.Second {
		n.aptRunning = n.lockHeld()
		if n.aptRunning || n.packagesChanged() {
			n.updLastCheck = time.Time{}
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

// checkUpdates runs a simulated upgrade, keeping the previous count if that fails.
func (n *notifier) checkUpdates() {
	out, err := n.simulate()
	if err != nil {
		n.logger.Printf("can't check for updates: %v", err)
		return
	}

	count, ok := parseSummary(out)
	if !ok {
		n.logger.Print("can't check for updates: no summary line in apt-get output")
		return
	}

	n.updAvailable = count
}

// Check if updates are available (every 5 minutes).
func (n *notifier) updatesAvailable() int {
	if n.now().Sub(n.updLastCheck) >= 5*time.Minute {
		n.checkUpdates()

		n.updLastCheck = n.now()
	}

	return n.updAvailable
}

// tick is one pass of the main loop.
func (n *notifier) tick() {
	switch {
	case n.isAptRunning():
		n.tray.hide()
	case n.updatesAvailable() > 0:
		n.userNotify()
	default:
		n.tray.hide()
		n.updNotified = 0 // whatever comes next is news, even if the count is the same as before
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

// serve shows the tray icon and keeps it up to date, forever.
func serve() {
	// https://github.com/mattn/go-gtk/issues/251
	gtk.Init(nil)
	glib.SetApplicationName("update-notifier")

	icon := gtk.NewStatusIconFromFile("/usr/share/icons/gnome/24x24/status/software-update-available.png")
	icon.SetTitle("update-notifier")
	icon.SetVisible(false)

	n := &notifier{
		tray:       gtkTray{icon: icon},
		locksFile:  procLocks,
		lockFiles:  packageLocks,
		statusFile: dpkgStatus,
		simulate:   func() (string, error) { return aptSimulate(aptGet) },
		now:        time.Now,
		logger:     log.Default(),
	}

	// Don't fight with system notifications during login.
	glib.TimeoutAdd(15000, func() bool {
		n.tick()

		glib.TimeoutAdd(1000, func() bool {
			n.tick()
			return true // keep the timer running
		})

		return false // the first check is done only once
	})

	gtk.Main()
}

// run handles the command line, and calls start if there's nothing else to do.
func run(args []string, stdout, stderr io.Writer, start func()) int {
	flags := flag.NewFlagSet("update-notifier", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // messages are printed below
	version := flags.Bool("version", false, "")

	err := flags.Parse(args)
	if err == nil && flags.NArg() > 0 {
		err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	switch {
	case errors.Is(err, flag.ErrHelp):
		_, _ = fmt.Fprint(stdout, usage)

		return 0
	case err != nil:
		_, _ = fmt.Fprintf(stderr, "update-notifier: %v\n%s", err, usage)

		return 2
	case *version:
		_, _ = fmt.Fprintf(stdout, "update-notifier %s\n", Version)

		return 0
	}

	start()

	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, serve))
}
