# Lavorare a mterm

Come si sviluppa questo progetto: gli accordi, le decisioni già prese e cosa
controllare prima di registrare una modifica. Un clone più questo file sono
tutto il contesto di lavoro: niente di ciò che serve per continuare sta fuori
dal repository.

## Come si lavora

- **Si parla con l'utente in italiano, sempre.** L'utente non capisce
  l'inglese: niente frasi di passaggio in inglese, nemmeno brevi. Anche
  codice, commenti, messaggi del programma e documentazione sono in italiano.
- **Testi per l'utente senza gergo da programmatori**: guida integrata,
  messaggi e manuale utente sono scritti per chi usa il programma, non per chi
  lo ha scritto.
- **Verificare, non dichiarare.** Ogni affermazione sul comportamento viene da
  un'esecuzione: un test end-to-end o una prova dal vivo in un terminale.
  "Dovrebbe funzionare" non è un risultato.
- **La documentazione conta quanto il codice.** Una funzione non è finita
  finché i manuali non la descrivono.

## Decisioni prese: non riaprirle

Il perché di ciascuna è in [docs/decisioni-e-storia.md](docs/decisioni-e-storia.md).

| Decisione | |
|---|---|
| **Nessuna emulazione di terminale** | Tubo di byte grezzi. Leggere l'output per tracciare uno stato (schermo alternato, modi del mouse) è ammesso; riscriverlo no. |
| **Un solo binario statico, zero dipendenze** | Solo libreria standard, `CGO_ENABLED=0`, ioctl diretti. |
| **Demone per sessione nello stesso eseguibile** | `mterm __daemon NOME`, socket e log in `$XDG_RUNTIME_DIR/mterm`. |
| **Un client alla volta** | Un nuovo attach sfratta il precedente. |
| **Tasti come `screen`** | Prefisso `Ctrl-a`. |
| **Buffer di replay + modi tracciati** | Il ripristino dello schermo non dipende dal ridisegno del programma; i modi DEC si riaffermano per finestra. |
| **L'eseguibile sta nel repository** | `./mterm` (Linux x86-64, statico) è registrato, così un `git clone` basta anche senza Go. `make` lo rigenera. |
| **Go 1.24** | Quello di Debian 13 (`golang-go`), il sistema del tablet. |
| **Nessuna licenza** | Copyright nic-fio, tutti i diritti riservati; il codice è pubblico per poterlo leggere e recuperare. |
| **Manuali in italiano, tema chiaro** | Stesso impianto di NG-EFI_SHELL e HOSTER (HTML in `docs/`, GitHub Pages). |

## Il repository

| Dove | Cosa |
|---|---|
| `*.go` | Il programma (`package main`): vedi la mappa dei file nel manuale tecnico. |
| `tests/e2e*.py` | Sei test end-to-end in Python su una PTY reale. |
| `docs/` | `manuale-utente.html`, `manuale-tecnico.html`, `decisioni-e-storia.md`, `assets/`. Pubblicati con GitHub Pages. |
| `tools/` | `setup-dev.sh` (pacchetti e identità git), `backup.sh` (bundle git), `check-docs.py` (controlli dei manuali). |
| `mterm` | L'eseguibile Linux x86-64, **registrato**: va rigenerato con `make` e registrato insieme a ogni modifica del codice. |
| `dist/` | Binari per le release, mai registrati. |

## Prima di registrare una modifica

1. `make test`: prima i controlli dei manuali sull'eseguibile registrato (ogni
   comando e tasto della guida integrata deve avere la sua voce `data-help` nel
   riferimento del manuale utente, ogni file `.go` e ogni test devono comparire
   nel manuale tecnico, la versione deve coincidere ovunque, anche dentro
   `./mterm`), poi ricostruisce `./mterm`, `go vet`, `gofmt` e tutti i test
   end-to-end. **Fallisce se i manuali non sono aggiornati.**
2. Registra anche `./mterm`, che deve corrispondere al sorgente.
3. Un difetto visibile corretto ha il suo test end-to-end in `tests/`, che
   fallisce senza la correzione.
4. Mai `git add -A` senza guardare: i file `mterm-*-w*-*.txt/.raw` salvati con
   `Ctrl-a s` sono ignorati, altro no.
5. I commit usano l'identità locale impostata da `tools/setup-dev.sh`
   (indirizzo noreply di GitHub), che tiene fuori quello personale.

Rilascio: aggiorna la versione in `help.go` (`"mterm 1.0"`), in `README.md`,
`docs/index.html` e nei due manuali, poi crea un tag annotato `vX.Y.Z` e fai
push del tag: la CI costruisce i binari Linux amd64/arm64 e pubblica la
release, con il messaggio del tag come note.

## A che punto è

Versione 1.0, completa. Punti aperti noti, descritti nel capitolo *Limiti noti*
del manuale tecnico: finestre oltre la 9 non raggiungibili con le cifre;
`mterm new` senza terminale lascia la sessione viva ma esce con stato 1; nomi di
sessione uguali ai comandi non raggiungibili con `mterm NOME`; permessi della
cartella dei socket non verificati se esiste già. Idee: overlay con più cifre,
rinomina di sessioni e finestre, `new -d`.
