# Copyright © 2018 Zlatko Čalušić
#
# Use of this source code is governed by an MIT-style license that can be found in the LICENSE file.
#

.PHONY: default update-notifier install uninstall clean

default: update-notifier

update-notifier:
	@go build -v

install: update-notifier
	sudo /usr/bin/install -m 755 update-notifier /usr/local/bin/update-notifier

uninstall:
	sudo rm -f /usr/local/bin/update-notifier

clean:
	rm -f update-notifier
