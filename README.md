# pushpop
Easily send files from one computer to another.

Repository layout (new idiomatic Go structure)

- `cmd/push`: `push` binary that shares a file via TCP + mDNS (zeroconf).
- `cmd/pop`: `pop` binary that discovers an mDNS announcement and downloads the file.
- `pkg/discovery`: helpers for mDNS discovery (extracting the `user` and matching IP).
- `pkg/transfer`: transfer logic (accepting connections, sending files, progress bar).

Quick Usage

1) Build:

```bash
go build ./cmd/push
go build ./cmd/pop
```

2) Send a file from machine A:

```bash
./cmd/push/push /path/to/file
```

3) On machine B, receive the file (optionally provide the username):

```bash
./cmd/pop/pop <username>
# or without argument it uses the USER environment variable
```

Notes

- The old `push/main.go` and `pop/main.go` files have been removed from the repository (their functionalities have been migrated under `cmd/`).

## TODO

- [ ] Be able to push a directory.
- [x] Be able to resume an interrupted download.
    - Use a `.part` suffix for partial downloads.
    - Resume only if the server supports HTTP Range requests; otherwise, restart from the beginning and warn the user.
    - Add a `--force` option to overwrite existing files without confirmation.
    - ~~Do not handle checksums for now (see below).~~
- [ ] Implement using multiple progress bars (e.g., `mpb`).
- [ ] ~~(Optional) Implement a mechanism to preallocate the final file size for downloads (file reservation).~~
- [x] (Optional) Add checksum/integrity verification after download (BLAKE3).
- [x] Add IP and username of the user downloading the file (when available) on the push side.
    - Client (`pop`) now sends the `X-PushPop-User` header.
    - Server (`push`) logs start/end of downloads with IP & username, and hash requests.
- [ ] Add a TUI (Terminal User Interface) to manage file sharing:
    - Allow the user to stop sharing a file manually.
    - Display active downloads and connections.
    - Blacklist users based on their IP address or username (when available).
- [ ] Add daemon mode for push:
    - Implement `--daemon` or `-d` flag to run push in the background (not default).
    - Create a control socket (e.g., `/tmp/pushpop.sock`) for daemon management.
    - Implement `push --control` to attach a TUI to a running daemon.
    - Implement `push --stop` to gracefully stop a running daemon.
    - Implement `push --status` to display information about active daemons.
- [ ] Display the user requesting a BLAKE3 in the push output.
- [ ] Fix display of the effective user in push (when pop is run from another machine, push should show the real user, not just the requested name).
- [ ] Update push to use Bubble Tea (TUI on the server side).
 - [ ] Add a progress bar for downloading the BLAKE3 verification file.
 - [ ] Add a progress bar for computing the BLAKE3 checksum (file parsing).
- [ ] It should be possible to bind to a list of interfaces on the push command. And on the pop command?
- [ ] Remove blake package if not used anymore.
- [ ] Pop: Move the Init()/Update()/View() functions into a tui.go file.
- [x] Pop: adapt the UI to the current window size.
- [ ] Use socket unix when downloading on the same machine.
- [ ] Provide multiple signature files: sha256/Blake3/etc.
- [ ] Better processing when the file exists and/or the part file exists.

## Known (unresolved) Issues:
- [ ] When you change the current window size, then the title line gets duplicated.