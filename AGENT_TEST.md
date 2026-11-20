# Testovanie SimpleMon agenta v Dockeri (Mac)

Tento návod opisuje, ako lokálne otestovať **Linuxovú** binárku SimpleMon agenta v Docker kontejnery na macOS.

> Mac == host, Docker kontajner == simulovaný Linux server.

---

## 1. Predpoklady

- Nainštalovaný **Docker Desktop** (alebo iný Docker engine na Macu)
- Nainštalované **Go** (kvôli buildu agenta)
- V tomto repozitári existuje súbor **`.env`** s premennou `BUILD_PATH`, napr.:

```env
BUILD_PATH=./build
```

---

## 2. Build Linux binárky agenta

V root adresári repozitára `agent` spusti:

```bash
./build.sh
```

Skript:

- načíta `.env`
- vytvorí adresár `BUILD_PATH`
- vybuildí viac bináriek, napr. (príklad pre Linux amd64):
  - `./build/simplemon-linux-amd64`

Pre účely testovania v Dockeri budeme používať **`simplemon-linux-amd64`**.

---

## 3. Príprava konfiguračného súboru

Vytvor si lokálny konfiguračný súbor, ktorý bude simulovať reálnu konfiguráciu na serveri.

V root adresári agenta vytvor `simplemon.conf`:

```json
{
  "serviceHost": "localhost:8092",
  "secure": false,
  "apiKey": "TVOJ_API_KEY",
  "serverToken": "TVOJ_SERVER_TOKEN",
  "verbose": true
}
```

Poznámky:

- `serviceHost` nastav tak, aby ukazoval na bežiace SimpleMon API (napr. lokálny Docker, dev/stage server, atď.).
- `secure` = `true`/`false` podľa toho, či komunikuješ cez HTTPS.
- `apiKey` a `serverToken` použiješ tie, ktoré reálne používa agent.

---

## 4. Jednoduchý Dockerfile na testovanie

V adresári `agent` vytvor súbor `Dockerfile.test` s obsahom:

```dockerfile
FROM debian:12

# Závislosti, ak agent niečo potrebuje (prípadne môžeš pridať ďalšie balíčky)
RUN apt-get update && apt-get install -y \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Skopíruj binárku a konfig do kontajnera
COPY build/simplemon-linux-amd64 /usr/local/bin/simplemon-agent
COPY simplemon.conf /etc/simplemon.conf

RUN chmod +x /usr/local/bin/simplemon-agent

# Štandardný entrypoint – spustenie agenta
ENTRYPOINT ["/usr/local/bin/simplemon-agent"]
```

> Ak máš iný `BUILD_PATH`, uprav cestu v `COPY` direktíve (`build/...`).

---

## 5. Build Docker image

V adresári `agent` spusti:

```bash
# 1) Build binárky (ak si ešte nerobil)
./build.sh

# 2) Build testovacieho Docker image
docker build -f Dockerfile.test -t simplemon-agent-test .
```

Po dokončení buildu by si mal vidieť image `simplemon-agent-test`:

```bash
docker images | grep simplemon-agent-test
```

---

## 6. Spustenie agenta v Docker kontejnery

Najjednoduchší spôsob:

```bash
docker run --rm \
  --name simplemon-agent-test \
  simplemon-agent-test
```

- Kontajner spustí binárku `simplemon-agent` s configom z `/etc/simplemon.conf`.
- Logy uvidíš priamo v termináli.

---

## 7. Alternatíva: mount binárky a configu z hosta (bez Dockerfile)

Ak nechceš mať extra Dockerfile, môžeš to spraviť aj priamo cez `docker run`:

```bash
./build.sh  # pre istotu, aby bol build aktuálny

# Príklad s Debian base image
docker run --rm \
  --name simplemon-agent-test \
  -v "$(pwd)/build/simplemon-linux-amd64:/usr/local/bin/simplemon-agent" \
  -v "$(pwd)/simplemon.conf:/etc/simplemon.conf" \
  debian:12 \
  /usr/local/bin/simplemon-agent
```

Takto:

- Image `debian:12` stiahne Docker automaticky (ak ešte nie je lokálne).
- Binárka a config sa `mountnú` z hosta do kontajnera.
- Kontajner spustí `/usr/local/bin/simplemon-agent` s tvojím configom.

---

## 8. Debugging a tipy

- Ak potrebuješ **interaktívnu shell session** v kontajneri:

  ```bash
  docker run --rm -it \
    -v "$(pwd)/build/simplemon-linux-amd64:/usr/local/bin/simplemon-agent" \
    -v "$(pwd)/simplemon.conf:/etc/simplemon.conf" \
    debian:12 \
    bash
  ```

  Potom vnútri:

  ```bash
  /usr/local/bin/simplemon-agent
  ```

- Sleduj logy a HTTP traffic medzi agentom a SimpleMon API (napr. `tcpdump`, `mitmproxy`, alebo priamo API logy).
- Na Macu je bežné, že API beží v inom Docker kontejnery (docker compose) – vtedy použi ako `serviceHost` DNS meno/alias toho kontajnera v rámci tej istej Docker siete.

---

Týmto spôsobom vieš bezpečne a rýchlo testovať správanie agenta na simulovanom Linux prostredí priamo z macOS bez nutnosti mať reálny Linux server.
