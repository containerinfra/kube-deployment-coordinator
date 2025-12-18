#!/bin/bash
# Generate self-signed certificates for webhook server local development
# Usage: ./hack/generate-webhook-certs.sh [output-dir]

set -e

CERT_DIR="${1:-/tmp/k8s-webhook-server/serving-certs}"
CERT_NAME="tls"

echo "Generating webhook certificates in ${CERT_DIR}..."

mkdir -p "${CERT_DIR}"

# Generate private key
openssl genrsa -out "${CERT_DIR}/${CERT_NAME}.key" 2048

# Generate certificate signing request
openssl req -new -key "${CERT_DIR}/${CERT_NAME}.key" \
  -out "${CERT_DIR}/${CERT_NAME}.csr" \
  -subj "/CN=webhook-server/O=system"

# Generate self-signed certificate (valid for 365 days)
openssl x509 -req -days 365 -in "${CERT_DIR}/${CERT_NAME}.csr" \
  -signkey "${CERT_DIR}/${CERT_NAME}.key" \
  -out "${CERT_DIR}/${CERT_NAME}.crt"

# Clean up CSR
rm "${CERT_DIR}/${CERT_NAME}.csr"

echo "Certificates generated successfully!"
echo "Certificate directory: ${CERT_DIR}"
echo ""
echo "To use these certificates, run:"
echo "  go run ./cmd/main.go --webhook-cert-dir=${CERT_DIR}"

