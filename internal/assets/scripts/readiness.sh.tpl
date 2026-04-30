#!/bin/bash
set -e
mongosh --quiet --port {{.Port}} --eval "db.adminCommand('ping')" > /dev/null 2>&1
