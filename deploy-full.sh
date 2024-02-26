#!/bin/sh -e
make
scp buttler-bot root@obsidian:/tmp/buttler-bot
scp deploy/buttler-bot.conf root@obsidian:/etc/buttler/buttler-bot.conf
scp deploy/buttler-bot.service root@obsidian:/etc/systemd/system/buttler-bot.service
ssh root@obsidian "bash -c 'mv /tmp/buttler-bot /usr/local/bin/buttler-bot; systemctl daemon-reload; systemctl restart buttler-bot.service'"
