# mterm — decisioni e storia

> Perché mterm è fatto così: il contesto, le decisioni e la storia del progetto.
> Il funzionamento in dettaglio è nel [manuale tecnico](https://nic-fio.github.io/MTERM/manuale-tecnico.html),
> l'uso nel [manuale utente](https://nic-fio.github.io/MTERM/manuale-utente.html).
> Stato: **completo** (versione 1.0, luglio 2026).

---

## 1. Cos'è

`mterm` è un gestore di sessioni di terminale con **detach/reattach**, pensato
come rimpiazzo moderno e minimale di GNU `screen`: un **singolo binario statico
Go, zero dipendenze**. Una sessione contiene una o più finestre (shell o
programmi) che restano vive quando il terminale si chiude; ci si riattacca e si
ritrova lo schermo com'era.

```
terminale <─byte grezzi─> [ mterm attach ] <─socket UNIX─> [ demone ] <─PTY─> shell / programma
```

## 2. Contesto e genesi

- Il problema: dentro `screen` la TUI di **Claude Code** (e in generale i
  programmi moderni a tutto schermo) si vedeva male: colori a 24 bit
  approssimati, sequenze di escape riscritte o scartate.
- La causa è strutturale: `screen` fa **emulazione di terminale completa**. Ha
  un parser VT100 interno e un buffer dello schermo, e rigenera l'output con
  `TERM=screen`. Tutto ciò che il suo emulatore non conosce si perde.
  `tmux` ha lo stesso impianto.
- Serviva solo il detach/reattach, non un secondo terminale in mezzo. Da qui
  mterm: stesso uso di `screen` (anche gli stessi tasti `Ctrl-a`), ma senza
  emulazione.

## 3. Decisioni

### 3.1 Nessuna emulazione di terminale

**Decisione:** mterm è un tubo di byte grezzi tra il terminale reale e la PTY.
Non interpreta l'output per ridisegnarlo.

**Perché:** è l'unico modo per garantire che un programma dentro mterm si
comporti come fuori, oggi e con le sequenze che i terminali introdurranno
domani. `TERM` resta quello dell'utente.

**Conseguenze accettate:** niente divisione dello schermo, niente modalità di
scorrimento interna, replay non adattato se la dimensione cambia (vedi *Limiti
noti* nel manuale tecnico). Leggere l'output per tracciare uno **stato**
(schermo alternato, modi del mouse) è ammesso; modificarlo no.

### 3.2 Un demone per sessione, nello stesso eseguibile

**Decisione:** `mterm new` rilancia sé stesso come `mterm __daemon NOME` in una
nuova sessione POSIX, con i descrittori su `/dev/null`. Il demone possiede le
PTY; un socket UNIX per sessione in `$XDG_RUNTIME_DIR/mterm`.

**Perché:** un solo file da installare; ogni sessione è isolata (un crash ne
tocca una sola); `ls` e `kill` sono banali (un file per sessione).

### 3.3 Un client alla volta

**Decisione:** un nuovo attach sfratta il precedente.

**Perché:** con un tubo grezzo non c'è un modo corretto di mostrare lo stesso
flusso a due terminali di dimensioni diverse. Lo sfratto copre il caso reale:
la sessione lasciata aperta sul PC e ripresa dal tablet.

### 3.4 Barra di stato con il trucco «righe−1»

**Decisione:** il client dichiara alla PTY una riga in meno, imposta la regione
di scorrimento `[1..righe−1]` e disegna la barra sull'ultima riga fisica.

**Perché:** una barra senza emulazione. Il programma non scrive mai
sull'ultima riga, e la shell non la trascina scorrendo.

**Correzione successiva:** molti programmi azzerano la regione uscendo, e la
barra veniva «sporcata». Ora il client riafferma la regione a ogni ridisegno
della barra sullo schermo normale, mai su quello alternato (test `e2e4.py`).

### 3.5 Buffer di replay invece del ridisegno

**Problema:** al reattach lo schermo restava vuoto. Si contava sul `SIGWINCH`
perché il programma si ridisegnasse, ma una shell ferma non lo fa, e Claude
Code ridisegna solo una parte.

**Decisione:** il demone conserva per ogni finestra gli ultimi 5 MiB di output e
li riproduce al reattach e al cambio finestra, poi forza comunque un
`SIGWINCH`. Sono gli stessi byte: non è emulazione (test `e2e5.py`).

### 3.6 Igiene dei modi del terminale

**Problema:** dopo aver usato una TUI con il mouse in una finestra, passando a
un'altra (o staccandosi) ogni movimento del mouse diventava una raffica di
caratteri a caso nella shell.

**Decisione:** il demone traccia per finestra un insieme fisso di modi DEC
privati (mouse, focus, bracketed paste, cursore, schermo alternato) e li
riafferma prima del replay; al distacco il client li spegne tutti. La scansione
regge le sequenze spezzate tra due letture (test `e2e6.py`).

### 3.7 Rendering a burst

**Decisione:** il client coalizza i frame pronti in una sola scrittura e
ridisegna la barra solo quando l'output si ferma (12 ms), dentro il
*Synchronized Update Mode*.

**Perché:** iniettare la barra in mezzo a un flusso fitto provocava sfarfallio
e rischiava di spezzare le sequenze dell'app.

### 3.8 Diagnostica nel log

**Decisione:** ogni demone scrive `NOME.log` accanto al socket (avvio,
attach/detach, errori, panic con stack); `SIGTERM`/`SIGHUP` chiudono in modo
pulito.

**Perché:** un processo senza terminale che sparisce non lascia altrimenti
nessuna traccia del perché.

### 3.9 Salvataggio dello schermo (`Ctrl-a s`)

**Decisione:** due file, `.raw` (byte esatti, con i colori) e `.txt` (testo
ripulito con una logica di riga minima).

**Perché:** senza un modello dello schermo non si può produrre «l'immagine»
dello schermo; il `.raw` conserva tutto, il `.txt` è quello che si legge e si
cerca.

### 3.10 Nessuna dipendenza, solo Linux

**Decisione:** solo libreria standard di Go; PTY, raw mode e dimensioni con
`syscall` e ioctl diretti; `CGO_ENABLED=0`.

**Perché:** un eseguibile statico che si copia e funziona, anche su un sistema
appena installato. Linux è l'unico sistema di destinazione.

### 3.11 Italiano, test end-to-end

Programma, messaggi, guida, commenti e documentazione sono in italiano, come
per gli altri progetti dello stesso autore. I test sono end-to-end in Python
(solo libreria standard) su una PTY reale, perché le proprietà che contano sono
quelle viste da un terminale.

## 4. Migrazione nel repository pubblico (settembre 2026)

Fino a settembre 2026 mterm esisteva solo come cartella sul tablet, senza
controllo di versione. Il 24 settembre 2026 è stato pubblicato nel repository
pubblico **nic-fio/MTERM**, sullo stesso impianto di NG-EFI_SHELL e HOSTER,
con l'obiettivo esplicito che in caso di guasto del tablet **un `git clone`
basti a recuperare tutto**:

- **l'eseguibile pronto è registrato** nel repository (`./mterm`, Linux
  x86-64), così il clone funziona anche senza Go; `check-docs.py` controlla che
  esista e che la sua versione coincida;
- `go.mod` portato da Go 1.26 a **Go 1.24**: nessuna funzione richiedeva la
  1.26, l'eseguibile era già stato compilato con la 1.24.4, e la 1.24 è quella
  del pacchetto `golang-go` di Debian 13, il sistema del tablet;
- **manuale utente e manuale tecnico** in HTML in `docs/`, pubblicati con
  GitHub Pages; `CLAUDE.md` con gli accordi di lavoro;
- `tools/setup-dev.sh`, `tools/backup.sh` (git bundle), `tools/check-docs.py`;
  CI con release dei binari amd64 e arm64 ai tag `v*`;
- `make test` ora lancia **tutti** i test (prima `e2e6.py` era escluso);
- corretti nella guida integrata l'esempio di `mterm` senza argomenti (diceva
  «apre questa guida», invece apre la sessione `Default`) e la sezione *File*,
  che non citava il log;
- **nessuna licenza**: copyright nic-fio, tutti i diritti riservati.

## 5. Storia

Ricostruita dalle date dei file (prima della migrazione non c'era controllo di
versione): le date sono affidabili, l'attribuzione di ogni funzione a un giorno
è approssimativa.

| Quando | Cosa |
|---|---|
| 1 luglio 2026 | Prima versione: PTY e raw mode con ioctl diretti, demone per sessione, detach/reattach, guida a schede. Test `e2e.py` (ciclo base), `e2e2.py` (truecolor, righe−1, sfratto). |
| 1 luglio 2026 | Finestre multiple e sessione `Default` (`e2e3.py`); regione di scorrimento riaffermata (`e2e4.py`). |
| 2 luglio 2026 | Buffer di replay e log del demone (`e2e5.py`); igiene dei modi DEC (`e2e6.py`); `Makefile`. |
| 9 luglio 2026 | Salvataggio dello schermo `Ctrl-a s` (`.txt` e `.raw`), rendering a burst, rifinitura di README e guida. |
| 24 settembre 2026 | Migrazione nel repository pubblico `nic-fio/MTERM`, manuali, CI, release v1.0.0. |
