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
- [ ] Be able to resume an interrupted download.
	- Use a `.part` suffix for partial downloads.
	- Resume only if the server supports HTTP Range requests; otherwise, restart from the beginning and warn the user.
	- Add a `--force` option to overwrite existing files without confirmation.
	- Do not handle checksums for now (see below).
- [ ] Implement using multiple progress bar (e.g. `mpb`).
- [ ] (Optional) Implement a mechanism to preallocate the final file size for downloads (file reservation).
- [ ] (Optional) Add checksum/integrity verification after download.
