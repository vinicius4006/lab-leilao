# Leilão — fechamento automático (Go Expert)

Sistema de leilões em Go (Gin + MongoDB). Ao criar um leilão, uma goroutine é
iniciada em background e, quando a duração configurada expira, atualiza o
leilão no banco para o status fechado (`Completed`, valor `1`), sem bloquear a
requisição de criação.

> No código o status "fechado" é `auction_entity.Completed` (`Active = 0`,
> `Completed = 1`).

## Como funciona

- `internal/infra/database/auction/create_auction.go`
  - `CreateAuction` insere o leilão e dispara `go closeAuctionAfter(...)`.
  - `closeAuctionAfter` espera a duração e executa um `UpdateOne` com filtro
    `{_id, status: Active}` mudando para `Completed` (idempotente), usando um
    contexto próprio (independente da requisição).
  - `RescheduleOpenAuctions` é chamado na inicialização e reagenda o fechamento
    dos leilões ainda abertos, para não perdê-los caso a aplicação reinicie.
  - `GetAuctionDuration` lê a duração das variáveis de ambiente.
- A validação de lances (`bid/create_bid.go`) usa a mesma duração, então
  lances são recusados a partir do fim do leilão.

## Variáveis de ambiente

Definidas em `cmd/auction/.env`:

| Variável | Descrição | Exemplo | Padrão |
|---|---|---|---|
| `AUCTION_DURATION` | Duração do leilão até o fechamento automático. Formato de duração do Go (`ns`, `us`, `ms`, `s`, `m`, `h`). | `20s`, `5m`, `1h30m` | `5m` |
| `AUCTION_INTERVAL` | Nome legado, usado apenas se `AUCTION_DURATION` não for válida. | `20s` | — |
| `MONGODB_URL` | String de conexão do MongoDB. | `mongodb://admin:admin@mongodb:27017/auctions?authSource=admin` | — |
| `MONGODB_DB` | Nome do banco. | `auctions` | — |

Para alterar o tempo, edite `AUCTION_DURATION` em `cmd/auction/.env` e suba a
aplicação novamente.

## Como rodar (Docker)

```bash
docker compose up --build
```

A API fica em `http://localhost:8080` e o MongoDB em `localhost:27017`.

### Exemplo

```bash
# 1. criar um leilão (condition: 1 = New, 2 = Used, 3 = Refurbished)
curl -X POST localhost:8080/auction \
  -H 'Content-Type: application/json' \
  -d '{"product_name":"iPhone 12","category":"phones","description":"iPhone 12 usado em bom estado","condition":2}'

# 2. pegue o id do leilão e consulte: imediatamente "status": 0 (aberto)
curl localhost:8080/auction
curl localhost:8080/auction/<id>

# 3. após AUCTION_DURATION: "status": 1 (fechado)
curl localhost:8080/auction/<id>
curl 'localhost:8080/auction?status=1'
```

## Como rodar localmente (sem Docker para a aplicação)

```bash
docker compose up -d mongodb
# o .env aponta para o host "mongodb"; para rodar localmente use localhost:
MONGODB_URL='mongodb://admin:admin@localhost:27017/auctions?authSource=admin' go run cmd/auction/main.go
```

## Testes

Os testes de integração usam um MongoDB real (e são pulados caso ele não esteja
acessível). Cada teste cria e remove um banco próprio.

```bash
docker compose up -d mongodb
go test ./internal/infra/database/auction/... -race -v
```

Por padrão conectam em `mongodb://admin:admin@localhost:27017/?authSource=admin`;
use `TEST_MONGODB_URL` para outro endereço.

`TestCreateAuctionClosesAutomatically` cobre o cenário do desafio: cria um
leilão (status aberto), aguarda a duração configurada e verifica que o status
passou para `Completed` sem intervenção manual.
