# Run DHCP

```shell
docker run --rm -d \
  --cap-add NET_ADMIN \
  --network host \
  --privileged \
  --device /dev/net/tun:/dev/net/tun \
  $(docker build -q .) \
  -n "${INTERFACE_NAME}"
```
