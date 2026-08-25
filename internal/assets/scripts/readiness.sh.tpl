#!/bin/bash
set -e
mongosh --norc --quiet --port {{.Port}} --eval "db.adminCommand('ping')" > /dev/null 2>&1
