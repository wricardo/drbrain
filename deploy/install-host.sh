#!/bin/sh
# One-time host provisioning for the Dr Brain monorepo. Idempotent; safe to
# re-run. Deploys afterwards are just release swaps (see .github/workflows/deploy.yml).
#
# Required inputs:
#   CANONICAL_HOST=drbrain.wricardo.net
#   MAZES_HOST=mazes.wricardo.net
#   MOTOR_HOST=motor-programming.wricardo.net
#   TESLA_HOST=tesla.wricardo.net
#   DELIVERY_HOST=delivery.wricardo.net
# Optional: TLS_DIR (default /etc/letsencrypt/live)
#
# Run as root from the repo root after the first release is installed.
set -eu

: "${CANONICAL_HOST:?set CANONICAL_HOST}"
: "${MAZES_HOST:?set MAZES_HOST}"
: "${MOTOR_HOST:?set MOTOR_HOST}"
: "${TESLA_HOST:?set TESLA_HOST}"
: "${DELIVERY_HOST:?set DELIVERY_HOST}"
TLS_DIR="${TLS_DIR:-/etc/letsencrypt/live}"

STATE_GAMES="mazes motor tesla delivery"

id drbrain >/dev/null 2>&1 || useradd --system --home /opt/drbrain --shell /usr/sbin/nologin drbrain
install -d -o drbrain -g drbrain /opt/drbrain /opt/drbrain/releases
for game in $STATE_GAMES; do
	install -d -o drbrain -g drbrain "/var/lib/drbrain/$game"
done
install -d -o drbrain -g drbrain /var/lib/drbrain/motor/maps-user /var/lib/drbrain/motor/accounts
install -d -o drbrain -g drbrain /var/lib/drbrain/tesla/maps-custom
install -d -o root -g root /etc/drbrain

if [ ! -f /etc/drbrain/drbrain.env ]; then
	install -m 0640 -o root -g drbrain deploy/drbrain.env.example /etc/drbrain/drbrain.env
	echo "install: wrote /etc/drbrain/drbrain.env — edit the paths, origins and secrets" >&2
else
	echo "install: keeping existing /etc/drbrain/drbrain.env" >&2
fi

install -m 0644 deploy/drbrain.service /etc/systemd/system/drbrain.service
systemctl daemon-reload

command -v envsubst >/dev/null || { echo "install: envsubst missing (apt install gettext-base)" >&2; exit 1; }
render() {
	CANONICAL_HOST="$CANONICAL_HOST" MAZES_HOST="$MAZES_HOST" MOTOR_HOST="$MOTOR_HOST" \
	TESLA_HOST="$TESLA_HOST" DELIVERY_HOST="$DELIVERY_HOST" TLS_DIR="$TLS_DIR" \
	envsubst '${CANONICAL_HOST} ${MAZES_HOST} ${MOTOR_HOST} ${TESLA_HOST} ${DELIVERY_HOST} ${TLS_DIR}' \
	< "$1" > /etc/nginx/sites-available/drbrain.conf
	ln -sfn /etc/nginx/sites-available/drbrain.conf /etc/nginx/sites-enabled/drbrain.conf
}

# Remove the four per-game vhosts once their legacy names redirect here.
for legacy in drbrain-mazes drbrain-motor-programming tesla-road-trip-game drbrain-delivery; do
	rm -f "/etc/nginx/sites-enabled/$legacy.conf"
done

install -d -o www-data -g www-data /var/www/certbot

# Certificates cannot be loaded before they exist, and certbot cannot pass
# --nginx validation against a config with missing certificates: start with the
# HTTP-only vhost, issue the certificate with the webroot, then switch to TLS.
render deploy/nginx/drbrain-bootstrap.conf.tmpl
nginx -t
systemctl reload nginx

CERT="$TLS_DIR/$CANONICAL_HOST/fullchain.pem"
if [ ! -f "$CERT" ]; then
	if [ -n "${ACME_EMAIL:-}" ] && command -v certbot >/dev/null; then
		certbot certonly --webroot -w /var/www/certbot \
			--cert-name "$CANONICAL_HOST" \
			-d "$CANONICAL_HOST" -d "$MAZES_HOST" -d "$MOTOR_HOST" \
			-d "$TESLA_HOST" -d "$DELIVERY_HOST" \
			--non-interactive --agree-tos -m "$ACME_EMAIL"
	else
		echo "install: no certificate at $CERT." >&2
		echo "install: DNS the five names here, then either re-run with ACME_EMAIL=you@example.com" >&2
		echo "install: or run: certbot certonly --webroot -w /var/www/certbot --cert-name $CANONICAL_HOST \\" >&2
		echo "  -d $CANONICAL_HOST -d $MAZES_HOST -d $MOTOR_HOST -d $TESLA_HOST -d $DELIVERY_HOST" >&2
		echo "install: HTTP-only vhost is live; re-run this script after certbot succeeds for TLS." >&2
		exit 0
	fi
fi

render deploy/nginx/drbrain.conf.tmpl
nginx -t
systemctl reload nginx

cat <<EOF

install: provisioned with TLS. Remaining one-time steps:
  1. DNS: $CANONICAL_HOST and the four legacy names -> this host.
  2. Stop and disable the four old units:
       systemctl disable --now drbrain-mazes drbrain-motor-programming tesla-road-trip-game drbrain-delivery
  3. systemctl enable drbrain.service
EOF
