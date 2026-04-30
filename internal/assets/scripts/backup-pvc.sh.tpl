
set -e
BACKUP_NAME="{{.ClusterName}}-$(date +%Y%m%d-%H%M%S)"
echo "Starting backup: ${BACKUP_NAME}"
mongodump --uri="${MONGODB_URI}" --out="/backup/${BACKUP_NAME}" {{.CompressionFlag}}
echo "Backup completed: ${BACKUP_NAME}"
