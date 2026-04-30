
set -e
BACKUP_NAME="{{.ClusterName}}-$(date +%Y%m%d-%H%M%S)"
echo "Starting backup: ${BACKUP_NAME}"

# Install aws-cli
apt-get update && apt-get install -y awscli

# Create backup and upload to S3
mongodump --uri="${MONGODB_URI}" {{.CompressionFlag}} --archive | \
    aws s3 cp - "s3://${S3_BUCKET}/${S3_PREFIX}${BACKUP_NAME}.archive.gz" \
    --endpoint-url="${S3_ENDPOINT}"

echo "Backup completed: ${BACKUP_NAME}"
