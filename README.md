# mterm

[![CI](https://github.com/nic-fio/MTERM/actions/workflows/ci.yml/badge.svg)](https://github.com/nic-fio/MTERM/actions/workflows/ci.yml)
[Documentazione](https://nic-fio.github.io/MTERM/) · [Download](https://github.com/nic-fio/MTERM/releases/latest)

Mini gestore di sessioni di terminale (**detach / reattach**), pensato come
rimpiazzo moderno e minimale di GNU `screen`: i programmi restano accesi quando
chiudi il terminale o cade la connessione, ti riattacchi e ritrovi tutto.

`screen` fa **emulazione di terminale completa**: un parser VT100 interno, un
buffer dello schermo e la rigenerazione dell'output con `TERM=screen`. È questo
doppio strato che maltratta gli strumenti moderni (truecolor perso, sequenze di
escape riscritte o scartate), per esempio la TUI di Claude Code.

`mterm` fa l'opposto: **nessuna emulazione**. È un tubo di byte grezzi tra il tuo
terminale e una pseudo-terminale (PTY), attraverso un socket UNIX. `TERM`, i
colori a 24 bit e le sequenze moderne passano **intatti**, quindi un programma a
tutto schermo dentro `mterm` funziona come funzionerebbe fuori.

```
   terminale  <──byte grezzi──>  [ mterm attach ]  <──socket UNIX──>  [ demone ]  <──PTY──>  shell / programma
```

## In breve

- **Un comando**: `mterm` apre (o riprende) la sessione `Default`;
  `Ctrl-a d` ti stacca lasciando tutto acceso.
- **Lo schermo torna com'era** al reattach, anche se il programma è fermo.
- **Più finestre** per sessione, con una barra di stato sull'ultima riga.
- **Salva lo schermo** su file con `Ctrl-a s` (testo e byte originali).
- **Un unico eseguibile statico**, zero dipendenze. Guida a schede: `mterm --help`.

## Documentazione

| Documento | Per |
|---|---|
| [Manuale utente](https://nic-fio.github.io/MTERM/manuale-utente.html) | Installare e usare mterm: sessioni, finestre, salvataggio, uso con ssh e Claude Code, problemi e soluzioni, tutti i comandi e i tasti. |
| [Manuale tecnico](https://nic-fio.github.io/MTERM/manuale-tecnico.html) | Come è fatto dentro: client e demone, protocollo, buffer di replay, modi del terminale, barra di stato, PTY, test, limiti noti. |
| [Decisioni e storia](docs/decisioni-e-storia.md) | Perché mterm è fatto così. |

I manuali sono pagine HTML, che GitHub mostra come codice sorgente: leggili
online su **https://nic-fio.github.io/MTERM/**, oppure apri
`docs/manuale-utente.html` nel browser da un clone (funzionano anche senza rete).

## Installazione

**Il programma pronto è nel repository**: il file `mterm` (Linux x86-64, PC e
tablet) è già compilato. Clonare basta:

```sh
git clone https://github.com/nic-fio/MTERM.git
cd MTERM
./mterm --help
make install            # facoltativo: copia mterm in ~/.local/bin
```

**Solo il programma, senza clonare**: dall'[ultima release](https://github.com/nic-fio/MTERM/releases/latest)
scarica `mterm-linux-amd64` (PC e tablet x86-64) o `mterm-linux-arm64`, poi:

```sh
mv mterm-linux-amd64 ~/.local/bin/mterm && chmod +x ~/.local/bin/mterm
```

**Dal sorgente** (serve Go 1.24, il pacchetto `golang-go` di Debian 13):

```sh
git clone https://github.com/nic-fio/MTERM.git
cd MTERM
tools/setup-dev.sh --install    # pacchetti (chiede sudo) e identità git
make && make test               # ricostruisce ./mterm, poi tutti i controlli
```

In alternativa a `make`: `./build.sh` (compila) e `./build.sh install`.

## Recupero dopo un guasto

Il repository contiene **tutto**: l'eseguibile pronto, sorgenti, test, manuali,
strumenti, la storia completa. mterm non ha file personali, quindi non c'è
niente da ricreare a mano. Su un altro computer o tablet:

```sh
git clone https://github.com/nic-fio/MTERM.git
cd MTERM && make install        # oppure: cp mterm ~/.local/bin/
```

Per ricostruirlo dal sorgente: `tools/setup-dev.sh --install && make test`.
`tools/backup.sh DIR` crea anche un backup su file (git bundle) che si
ripristina senza rete: `git clone mterm-AAAA-MM-GG.bundle MTERM`. I dettagli
sono nel capitolo *Recupero su un nuovo dispositivo* del manuale utente.

## Uso

```
mterm                               apre (o crea) la sessione predefinita «Default»
mterm new [nome] [-- comando ...]   crea una sessione e vi si attacca    (mterm n)
mterm attach NOME                   si riattacca a una sessione esistente (mterm a)
mterm ls                            elenca le sessioni attive
mterm kill NOME                     termina una sessione                  (mterm k)
mterm NOME                          se NOME esiste ci si attacca, altrimenti la crea
mterm --help                        guida a schede                        (mterm -h)
```

| Tasto | Azione |
|---|---|
| `Ctrl-a d` | Stacca la sessione (i processi continuano in background). |
| `Ctrl-a c` | Crea una nuova finestra (nuova shell) e vi passa. |
| `Ctrl-a n` / `Ctrl-a spazio` | Finestra successiva. |
| `Ctrl-a p` | Finestra precedente. |
| `Ctrl-a 0`…`9` | Va alla finestra con quel numero. |
| `Ctrl-a "` | Elenco finestre: premi il numero, `Esc` annulla. |
| `Ctrl-a k` | Chiude la finestra corrente (se è l'ultima, termina la sessione). |
| `Ctrl-a s` | Salva la finestra in `mterm-<sessione>-w<id>-<orario>.txt` e `.raw` nella cartella corrente. |
| `Ctrl-a Ctrl-a` | Invia un `Ctrl-a` letterale al programma. |

```sh
mterm new lavoro          # crea «lavoro» e ci entra
mterm n -- claude         # avvia direttamente claude in una sessione staccabile
mterm ls                  # quali sessioni sono attive
mterm a lavoro            # riattacca «lavoro»
mterm kill lavoro         # termina «lavoro»
```

## Lavorare al progetto

[CLAUDE.md](CLAUDE.md) raccoglie come si lavora al progetto: gli accordi, le
decisioni già prese e cosa controllare prima di registrare una modifica.

## Copyright

Copyright (c) 2026 nic-fio. **Tutti i diritti riservati**: il codice è
pubblico perché si possa leggere e recuperare, ma non è concessa alcuna
licenza d'uso, copia o modifica. I componenti di terzi mantengono le proprie
licenze, elencate in [NOTICE.md](NOTICE.md).

## Stato

Versione 1.0. Sei test end-to-end
che guidano il vero eseguibile attraverso una PTY reale: ciclo detach/reattach,
truecolor, finestre, barra di stato, ripristino dello schermo, igiene dei modi
del terminale.
