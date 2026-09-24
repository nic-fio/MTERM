# Documentazione di mterm

GitHub mostra i file `.html` come codice sorgente: non visualizza le pagine web
salvate in un repository. I manuali si leggono in uno di questi due modi.

**Online** (GitHub Pages, sempre allineati a `main`):

| Documento | Link |
|---|---|
| Manuale utente | https://nic-fio.github.io/MTERM/manuale-utente.html |
| Manuale tecnico | https://nic-fio.github.io/MTERM/manuale-tecnico.html |
| Pagina iniziale | https://nic-fio.github.io/MTERM/ |

**Senza rete**: clona il repository e apri `docs/manuale-utente.html` nel
browser. Tutto quello che serve alle pagine (stile, script, diagrammi) è in
`docs/assets`, quindi funzionano anche offline.

[Decisioni e storia](decisioni-e-storia.md) è in Markdown, quindi GitHub lo
mostra già formattato.

Dentro il programma, `mterm --help` mostra la guida integrata a schede.

## File

| File | Cosa |
|---|---|
| `index.html` | Pagina iniziale del sito della documentazione. |
| `manuale-utente.html` | Installare e usare mterm; riferimento di tutti i comandi e i tasti. |
| `manuale-tecnico.html` | Architettura, funzionamento interno, test, convenzioni, limiti noti. |
| `decisioni-e-storia.md` | Perché mterm è fatto così. |
| `assets/` | Foglio di stile, script e una copia locale di Mermaid (MIT). |

`tools/check-docs.py` (eseguito da `make test`) verifica che i manuali siano
allineati al codice.
