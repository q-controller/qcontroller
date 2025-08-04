FROM alpine:3.20 AS dhcp

RUN apk add --no-cache dnsmasq bash iptables

ADD dhcp-entrypoint.sh /usr/bin/dhcp-entrypoint.sh

ENTRYPOINT ["/usr/bin/dhcp-entrypoint.sh"]
