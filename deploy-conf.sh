#!/bin/sh -e
scp deploy/buttler-bot.conf root@obsidian:/etc/buttler/buttler-bot.conf
ssh root@obsidian "systemctl restart buttler-bot.service"
