# pushpop
Easily send files from one computer to another.

Repository layout (nouvelle structure idiomatique Go)

- `cmd/push` : binaire `push` qui partage un fichier via TCP + mDNS (zeroconf).
- `cmd/pop`  : binaire `pop` qui découvre une annonce mDNS et télécharge le fichier.
- `pkg/discovery` : helpers pour la découverte mDNS (extraction du `user` et correspondance d'IP).
- `pkg/transfer`  : logique de transfert (acceptation de connexion, envoi de fichier, barre de progression).

Utilisation rapide

1) Construire :

```bash
go build ./cmd/push
go build ./cmd/pop
```

2) Envoyer un fichier depuis la machine A :

```bash
./cmd/push/push /chemin/vers/fichier
```

3) Sur la machine B, recevoir le fichier (optionnellement fournir le nom d'utilisateur) :

```bash
./cmd/pop/pop <username>
# ou sans argument il utilise la variable d'environnement USER
```

Notes

- Les anciens fichiers `push/main.go` et `pop/main.go` ont été retirés du dépôt (leurs fonctionnalités ont été migrées sous `cmd/`).
## TODO

- [ ] Be able to push a directory.
- [x] Be able to resume an interrupted download.
	- Use a `.part` suffix for partial downloads.
	- Resume only if the server supports HTTP Range requests; otherwise, restart from the beginning and warn the user.
	- Add a `--force` option to overwrite existing files without confirmation.
	- ~~Do not handle checksums for now (see below).~~
- [ ] Implement using multiple progress bar (e.g. `mpb`).
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
	- Implement `--daemon` or `-d` flag to run push in background (not default).
	- Create a control socket (e.g., `/tmp/pushpop.sock`) for daemon management.
	- Implement `push --control` to attach a TUI to a running daemon.
	- Implement `push --stop` to gracefully stop a running daemon.
	- Implement `push --status` to display information about active daemons.

