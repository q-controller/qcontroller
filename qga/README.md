## QGA build

To build a QGA agent, run

```shell
./build.sh --out-dir build --qemu-dir ../qapi-client/qemu
```

The build process runs inside a container to ensure all dependencies are installed. Additionally, the build uses [the same version of QEMU](../qapi-client/qemu) that was used to generate the QAPI client, ensuring compatibility between QGA and the QAPI client.
