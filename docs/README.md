# spotcontrol Documentation

**spotcontrol** is a Go library for controlling [Spotify Connect](https://www.spotify.com/connect/) devices. It implements Spotify's modern Connect protocol stack including access point (AP) authentication, Login5 token management, dealer WebSocket real-time messaging, spclient HTTP API, and Connect State device control.

This is a modernized rewrite based on the protocol details from [go-librespot](https://github.com/devgianlu/go-librespot) and the original [librespot](https://github.com/librespot-org/librespot) project. Spotcontrol focuses solely on **remote control** of other Spotify devices — it does not play music itself.

## Documentation Index

| Document | Description |
|----------|-------------|
| [Getting Started](getting-started.md) | Installation, quick start guide, and first steps |
| [Architecture](architecture.md) | System design, component overview, and protocol stack |
| [Authentication](authentication.md) | All supported authentication methods and credential management |
| [Package Reference](package-reference.md) | Detailed API documentation for every package |
| [Controller Guide](controller-guide.md) | High-level playback control, device management, and event subscriptions |
| [Protocol Details](protocol-details.md) | Low-level Spotify protocol internals (AP, Login5, Dealer, Mercury) |
| [Examples](examples.md) | Walkthrough of the included example applications |
| [Configuration](configuration.md) | All configuration options, state persistence, and logging |
| [Contributing](contributing.md) | Development setup, testing, protobuf generation, and project structure |

## Quick Links

- **Simplest way to get started**: [`quick.Connect()`](getting-started.md#one-liner-with-quickconnect)
- **Playback control API**: [Controller Guide](controller-guide.md)
- **Event subscriptions**: [Controller Guide — Events](controller-guide.md#event-subscriptions)
- **Track metadata**: [Controller Guide — Metadata](controller-guide.md#track-metadata)
- **CLI example**: [Examples — micro-controller](examples.md#micro-controller)
- **Event watcher example**: [Examples — event-watcher](examples.md#event-watcher)

## License

MIT License — see [LICENSE](../LICENSE).

## Disclaimer

Much of this code was written with the use of Claude Opus 4.6.