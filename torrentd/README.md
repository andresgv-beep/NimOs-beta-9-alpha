# NimOS Torrent Daemon (nimos-torrentd)

Native BitTorrent daemon for NimOS using libtorrent-rasterbar.
Communicates via Unix socket — no open ports.

## Dependencies

```bash
sudo apt install -y libtorrent-rasterbar-dev libboost-system-dev g++
```

## httplib.h

Download the single-header HTTP library:

```bash
cd torrentd/
curl -fsSLO https://raw.githubusercontent.com/yhirose/cpp-httplib/master/httplib.h
```

## Build

```bash
cd torrentd/
make
sudo make install
```

## Service

```bash
sudo cp nimos-torrentd.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now nimos-torrentd
```

## Architecture

```
NimOS UI → NimOS API (port 5000) → Unix socket → nimos-torrentd
                                    /run/nimos/torrent.sock
```

The NimOS API proxies torrent requests to the daemon.
Auth is handled by NimOS — the daemon has no auth layer.

## Files

- `main.cpp` — Daemon, Unix socket API, signal handling
- `torrent_engine.h/cpp` — libtorrent wrapper, persistence, state
- `torrent.conf` — Default config
- `nimos-torrentd.service` — systemd unit
- `httplib.h` — HTTP library (download separately)
