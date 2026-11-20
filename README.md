# simplemon-agent

Systémový monitorovací agent, ktorý zbiera metriky o CPU, pamäti, diskoch a I/O operáciách a odosiela ich na centrálny server.

## Obsah

- [Zbierané dáta](#zbierané-dáta)
- [Režimy zbierania](#režimy-zbierania)
- [WebSocket protokol](#websocket-protokol)
- [Health Endpoint](#health-endpoint)
- [Konfigurácia](#konfigurácia)
- [Inštalácia](#inštalácia)
- [Spustenie](#spustenie)
- [Verzovanie a aktualizácie](#verzovanie-a-aktualizácie)
- [Licencia](#licencia)

## Zbierané dáta

Agent zbiera nasledovné informácie o systéme:

### CPU metriky

| Metrika | Popis |
|---------|-------|
| `overall_usage` | Celkové využitie CPU v percentách (0-100%) |
| `core_usage` | Pole s využitím jednotlivých jadier |
| `processes` | Top 10 procesov podľa CPU využitia |

**Per-process CPU dáta:**
- `name` - Názov procesu
- `value` - CPU využitie v percentách
- `count` - Počet inštancií procesu s rovnakým názvom

### Memory metriky

| Metrika | Popis |
|---------|-------|
| `total` | Celková pamäť v bytoch |
| `available` | Dostupná pamäť v bytoch |
| `used` | Použitá pamäť v bytoch |
| `processes` | Top 10 procesov podľa využitia pamäte |

**Per-process Memory dáta:**
- `name` - Názov procesu
- `value` - RSS pamäť v bytoch
- `count` - Počet inštancií procesu

### Disk metriky

Pre každý fyzický mount point:

| Metrika | Popis |
|---------|-------|
| `path` | Cesta mount pointu |
| `type` | Typ (Physical) |
| `fstype` | Typ súborového systému (ext4, xfs, atď.) |
| `total` | Celková kapacita v bytoch |
| `free` | Voľné miesto v bytoch |
| `used` | Použité miesto v bytoch |

### I/O metriky

| Metrika | Popis |
|---------|-------|
| `total_read` | Celkový počet prečítaných bytov |
| `total_write` | Celkový počet zapísaných bytov |
| `processes` | Top 10 procesov podľa I/O aktivity |

**Per-process I/O dáta:**
- `name` - Názov procesu
- `value` - Kombinovaný read+write v bytoch
- `count` - Počet inštancií procesu

## Režimy zbierania

### Normálny režim (predvolený)

- **Interval zbierania:** konfigurovateľný (predvolene 1000ms)
- **Ukladaný interval:** vzorky z 1 s sa priemerujú do jednej vzorky za 10 s (`storeInterval`)
- **Procesy:** top procesy sa ukladajú raz za minútu; živý RT stream ich posiela každú sekundu
- **Batch interval:** konfigurovateľný (predvolene 10000ms)
- Metriky sa zbierajú lokálne a odosielajú v dávkach cez HTTP POST
- Dáta sú komprimované pomocou gzip

### Real-time režim

- Aktivuje sa príkazom `START` cez WebSocket
- Metriky sa streamujú **každú sekundu** cez WebSocket
- Automaticky sa vypne po **30 sekundách** bez `TICK` keepalive
- Deaktivuje sa príkazom `STOP`

**Upozornenie:** V real-time režime sa odosiela 5x viac dát ako v normálnom režime.

## WebSocket protokol

Agent komunikuje so serverom cez WebSocket na `/incoming` endpoint.

### Príkazy od servera

| Príkaz | Popis |
|--------|-------|
| `{"action": "START"}` | Aktivuje real-time streaming |
| `{"action": "STOP"}` | Deaktivuje real-time streaming |
| `{"action": "TICK"}` | Keepalive - musí prísť každých < 30 sekúnd |

### Správy od agenta

| Správa | Popis |
|--------|-------|
| `{"action": "ready"}` | Odoslaná po úspešnom odoslaní batch metrík |
| Metriky JSON | V real-time režime sa odosielajú metriky každú sekundu |

## Health Endpoint

Ak je nakonfigurovaný `healthPort`, agent poskytuje health check endpointy:

### GET /health

Vracia JSON so stavom agenta:

```json
{
  "status": "healthy",
  "wsConnected": true,
  "metricsCollected": 1234,
  "batchesSent": 100,
  "lastError": null,
  "hostname": "my-server"
}
```

### GET /ready

Jednoduchý readiness check - vracia `200 OK` ak je agent pripravený.

## Konfigurácia

### Konfiguračný súbor

Agent hľadá konfiguráciu v tomto poradí:
1. Cesta zadaná cez `-config` flag
2. `./simplemon.conf` (lokálny)
3. `/etc/simplemon.conf` (globálny)

### Formát konfigurácie (JSON)

```json
{
  "serviceHost": "monitoring.example.com:8080",
  "secure": true,
  "apiKey": "váš-api-kľúč",
  "serverToken": "váš-server-token",
  "verbose": false,
  "metricInterval": 1000,
  "batchInterval": 10000,
  "storeInterval": 10000,
  "healthPort": 8081
}
```

### Konfiguračné parametre

| Parameter | Typ | Predvolené | Popis |
|-----------|-----|------------|-------|
| `serviceHost` | string | *povinné* | Adresa servera (host:port) |
| `secure` | bool | `false` | Použiť HTTPS/WSS |
| `apiKey` | string | *povinné* | API kľúč pre autentifikáciu |
| `serverToken` | string | *povinné* | Server token pre autentifikáciu |
| `verbose` | bool | `false` | Zapnúť detailné logovanie |
| `metricInterval` | int | `1000` | Interval zbierania metrík v ms (100-60000) |
| `batchInterval` | int | `10000` | Interval odosielania batch v ms (1000-300000) |
| `storeInterval` | int | `10000` | Interval ukladanej (priemerovanej) vzorky v ms (metricInterval-300000) |
| `healthPort` | int | `0` | Port pre health endpoint (0 = vypnuté) |

### Command-line flags

```bash
./simplemon-agent [flags]
```

| Flag | Popis |
|------|-------|
| `-config <path>` | Cesta ku konfiguračnému súboru |
| `-apiKey <key>` | Prepísať API kľúč |
| `-serviceHost <host>` | Prepísať adresu servera |
| `-secure` | Použiť zabezpečené spojenie |
| `-verbose` | Zapnúť detailné logovanie |

## Inštalácia

### Zo zdrojového kódu

```bash
git clone https://github.com/yourusername/simplemon-agent
cd simplemon-agent
go build -o simplemon-agent ./cmd/simplemon-agent
```

### Docker

```bash
docker build -t simplemon-agent .
docker run -v /path/to/config:/etc/simplemon.conf simplemon-agent
```

## Spustenie

### Priame spustenie

```bash
./simplemon-agent -config ./simplemon.conf
```

### S verbose logovaním

```bash
./simplemon-agent -config ./simplemon.conf -verbose
```

### Ako systemd služba

Vytvorte súbor `/etc/systemd/system/simplemon-agent.service`:

```ini
[Unit]
Description=Simplemon Monitoring Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/simplemon-agent
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable simplemon-agent
sudo systemctl start simplemon-agent
```

## Verzovanie a aktualizácie

### Verzia binárky

Verzia sa injectuje pri builde cez ldflags. `build.sh` to robí automaticky:

```bash
./build.sh   # verzia = git tag (napr. v1.2.0), commit = short hash
```

Agent potom vie verziu povedať:

```bash
simplemon-agent -version
# simplemon-agent v1.2.0 (commit abc1234)
```

Build zároveň zapíše manifest `agent-version.json` do `BUILD_PATH` (verzia, commit, čas buildu). Panel ho používa na zobrazenie najnovšej dostupnej verzie.

### Hlásenie verzie serveru

Agent posiela verziu v hlavičke `X-Agent-Version` pri každom batchi metrík aj pri WebSocket pripojení. Ingest si ju uloží k serveru (stĺpec `agent_version`) a panel ju zobrazuje v nastaveniach servera spolu so statusom (aktuálna / zastaralá / nenahlásená).

### Kompatibilita verzií

Ingest pozná env premennú `AGENT_MIN_VERSION` (napr. `v1.4.0`). Ak agent pošle nižšiu verziu, server odpovie HTTP 426 Upgrade Required s JSON `{"error", "agent_version", "min_version"}`. Agent vtedy:

- prestane odosielať metriky (batch sa zahodí, retry nemá zmysel),
- loguje upozornenie raz za hodinu, aby nespamoval logy,
- beží ďalej (health endpoint funguje), kým sa neaktualizuje.

Agenti staršie ako táto funkcia (bez hlavičky) sa považujú za legacy a server ich prijme aj pri nastavenom `AGENT_MIN_VERSION`. Nečitateľnú verziu (`dev` build) server neodmietne.

### Auto-update

Inštalácia (panel `install.sh` aj `installers/linux/install.sh`) vytvorí:

- `/usr/local/bin/simplemon-agent-update` - skript, ktorý stiahne aktuálnu binárku z `BINARY_BASE_URL`, overí SHA256SUMS, porovná ju cez `cmp` s nainštalovanou a pri rozdieli vymení binárku a reštartuje službu,
- `simplemon-agent-update.service` + `simplemon-agent-update.timer` - denná kontrola (s randomizovaným delayom, `Persistent=true` dobehne zmeškané behy).

Update beží ako root cez systemd oneshot jednotku - agent proces sám root práva nemá a nepotrebuje. Manuálny upgrade stále funguje:

```bash
curl -fsSL https://<panel>/linux/upgrade.sh | sudo bash
```

Pri vydaní novej verzie teda stačí: `make deploy-agent VERSION=1.2.3` → servery sa aktualizujú do 24 h samé (alebo hneď po spustení upgrade skriptu).

## Formát odosielaných dát

```json
{
  "timestamp": "2025-05-07 18:38:20",
  "delta_time": 0.999788625,
  "memory": {
    "total": 4109737984,
    "available": 3105353728,
    "used": 766255104,
    "processes": [
      {"name": "myapp", "value": 13799424, "count": 1}
    ]
  },
  "cpu": {
    "overall_usage": 0.625,
    "core_usage": [0, 0, 0.99, 0, 1.01, 2.02, 0, 0],
    "processes": [
      {"name": "myapp", "value": 0.47, "count": 1}
    ]
  },
  "disks": [
    {
      "path": "/",
      "type": "Physical",
      "fstype": "ext4",
      "total": 33634926592,
      "free": 20814815232,
      "used": 11085402112
    }
  ],
  "io": {
    "total_read": 842799104,
    "total_write": 5597478912,
    "processes": [
      {"name": "myapp", "value": 35791710, "count": 1}
    ]
  }
}
```

## Systémové požiadavky

- **OS:** Linux (primárne), macOS (obmedzené)
- **Go:** 1.22+
- **Prístup:** Čítanie `/proc` súborového systému (Linux)

## Licencia

MIT License - viď [LICENSE](LICENSE)
