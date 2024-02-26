#!/bin/sh -e
make
scp buttler-bot root@obsidian:/tmp/buttler-bot
ssh root@obsidian "bash -c 'mv /tmp/buttler-bot /usr/local/bin/buttler-bot; systemctl restart buttler-bot.service'"
