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
- TODO existants :
	- [ ] Be able to push a directory.
	- [ ] Be able to resume an interrupted download.
	- [ ] Implement using multiple progress bar (par ex. `mpb`).
