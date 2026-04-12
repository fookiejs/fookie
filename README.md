# Fookie Framework

**A declarative backend framework** — write your business logic in Fookie Schema Language (FSL), the framework handles SQL compilation, external orchestration, transactions, and async workflows automatically.

## Quick Start

### Prerequisites
- Go 1.21+
- PostgreSQL 12+
- Docker & Docker Compose (optional)

### Local Development

```bash
# Clone and setup
git clone https://github.com/fookiejs/fookie.git
cd fookie

# Install dependencies
go mod download

# Build tools
make build

# Start PostgreSQL
docker-compose up -d postgres

# Run server
make run-server

# In another terminal, run worker
make run-worker
```

### Docker

```bash
# Start all services (postgres, server, worker)
make docker-up

# View logs
make docker-logs-server
make docker-logs-worker

# Stop services
make docker-down
```

## Project Structure

```
fookie/
├── cmd/
│   ├── server/      # HTTP API server
│   ├── worker/      # Async outbox worker
│   └── parser/      # FSL parser CLI tool
├── pkg/
│   ├── ast/         # Abstract Syntax Tree types
│   ├── parser/      # Lexer and Parser
│   ├── compiler/    # SQL code generation
│   └── runtime/     # Execution engine, external manager
├── schemas/         # Example FSL schemas
├── tests/           # Unit and integration tests
├── migrations/      # Auto-generated SQL migrations
└── docker/          # Docker build files
```

## Usage

### 1. Define Your Schema (FSL)

Create `schemas/transaction.fql`:

```fql
external ValidateToken {
  input {
    token: string
  }
  output {
    userId: id
    valid: boolean
  }
}

model Transaction {
  fields {
    amount: number
    fromWalletId: id
    toWalletId: id
  }

  create {
    role {
      principal = ValidateToken(token: input.token)
    }

    rule {
      input.amount > 0
      principal.userId != null
    }

    modify {
      amount = input.amount
    }

    effect {
    }
  }
}
```

### 2. Build & Deploy

```bash
# Validate schema
make run-parser

# Start server
make docker-up
```

### 3. Use the API

```bash
# Create transaction
curl -X POST http://localhost:8080/operations \
  -H "Content-Type: application/json" \
  -d '{
    "operation": "create",
    "model": "Transaction",
    "input": {
      "token": "jwt-token...",
      "amount": 100,
      "fromWalletId": "wallet-1",
      "toWalletId": "wallet-2"
    }
  }'
```

## Features

✅ **Declarative Schema** — Define your backend as code (FSL)
✅ **Automatic SQL Generation** — Compiles to optimized PostgreSQL
✅ **External Orchestration** — Manage API calls, retries, and timeouts
✅ **Outbox Pattern** — Reliable async processing with automatic retries
✅ **Transaction Lifecycle** — Auto-managed status progression (initiate → progress → done/failed)
✅ **Audit Logging** — Automatic audit trail and event logs
✅ **Role-Based Access** — Built-in auth and permission blocks
✅ **Docker Ready** — Run locally or deploy to Kubernetes

## Testing

```bash
# Run all tests
make test

# Run specific test suite
make test-parser    # Parser tests
make test-compiler  # SQL code generation tests
```

## Contributing

This is a framework/language project. Contributions welcome for:
- Parser improvements
- SQL optimization
- External manager enhancements
- Documentation
- Examples and tutorials

---

# Fookie Schema Language (FSL) — Specification

```md
# Fookie Schema Language (FSL) — Kısa Dokümantasyon

FSL, **SQL’e compile edilen** ve aynı zamanda **transaction + workflow + external orchestration** sağlayan deklaratif bir backend dilidir.

Dokümanda geçen **`User`**, **`PrivyVerify`**, **`SendMail`**, **`Auth`** vb. isimler **örnektir**; dil veya çerçeve belirli bir sağlayıcıya (Privy, e-posta servisi, …) bağlı değildir — kendi modellerinizi ve **`external`** sözleşmelerinizi tanımlarsınız.

---

# 🔤 Anahtar kelimeler (özet)

Üst seviye yapı taşları (özet liste): **`model`**, **`external`**, **`module`**, **`use`**, **`create`**, **`read`**, **`update`**, **`delete`**, **`config`**, …  

CRUD gövdesinde yalnızca: **`role`**, **`rule`**, **`modify`**, **`effect`**.  

Her operasyonda gövde bağlamı: **`input`** (gelen istek), persist sonrası kök satır için **`output`**.  

Sorgu / küme tarafında: **`where`**, **`orderBy`**, **`cursor`**, **`return`**, aggregate olarak **`sum`** (ve benzeri) — bunlar ilgili blok içinde kullanılır.

---

# 🧠 Temel Prensipler

- ✅ Okuma, filtre, aggregate, kalıcı yazma **öncelikle database’de** çalışır (SQL’e compile)
- ❌ Uygulama katmanında “el ile” liste üzerinde filtre / aggregate koşturma yok; bu işler DSL ile SQL tarafına gider
- ✅ Tanımlı **`external`** çağrıları istek içinde, **`modify` öncesinde** sonuç gerekiyorsa kullanılabilir — **gecikme ve bağımlılık** bilinçli tercihtir
- ✅ **`effect`** içindeki **`external`** çağrıları ve diğer fire-and-forget dış işler **outbox / worker** ile yürür (async yol)
- ✅ Sistem **deterministic + transactional** (dış adımlar da `external` sözleşmesiyle tanımlıdır)
- ✅ **Silme** yalnızca **soft delete** ile modellenir; satırı DB’den fiziksel silen ayrı bir yol yok — bunun açık/kapalı ve alan adı **`config`** üzerinden gelir, modele serpiştirilmez

---

# 🔥 Lifecycle Akışı

Her model yüzeyinde (**`create`**, **`read`**, **`update`**, **`delete`**) gövdede yalnızca şu bloklar vardır; başka üst seviye anahtar kelime yok: **`role`**, **`rule`**, **`modify`**, **`effect`**. (Kalıcı yazım **`modify`** sonrası örtük **persist**.)

```

role → rule → modify → persist → effect

````

| Blok | Açıklama |
|--------|---------|
| `role` | kimlik / yetki kökü (ör. `VerifyJwt`, bağlam değişkenleri) |
| `rule` | doğrulama; gerekirse **`read`** / **`external`** atamaları ve iş kuralları |
| `modify` | satır alanlarının yazımı |
| `effect` | async / outbox yan etkiler |

---

# 🎯 Context ve görünürlük

- Her istek **tek bir kök operasyon** ile açılır (`create User`, `update Order`, …). **Gelen isteğin gövdesi** (doğrulanan payload) her zaman **`input`** tipiyle temsil edilir. Kök satır DB’ye yazıldıktan sonra o satır **`output`** ile temsil edilir — özellikle **`effect`** ve aynı operasyondaki sonraki **`create`** / iç içe adımlarda kök entity’ye referans **`output.id`**, **`output.email`** vb. üzerinden gider. Başka bir isteğin verisi yoktur.
- Aynı transaction içindeki iç içe `create` / `update` / `read` adımları yalnızca şunlardan beslenir: kök **`input`**, ilgili CRUD’un **`role` / `rule`** gövdelerindeki **`isim = read \| create \| ExternalAdı({...})`** atamaları, persist sonrası **`output`**, ve dahil ettiğin **modülün** aynı dört blokta ürettiği yereller. İhtiyaç varsa **`x = read …`** veya **`x = PrivyVerify({ token: input.token })`** gibi açık çağrı **`rule`** (veya **`role`**) içinde yazılır.
- **`use PrivyAuth` ve `auth`:** `auth` modülün **`role`** bloğunda atanır — `module PrivyAuth { role { auth = PrivyVerify({ … }) } … }`. `PrivyVerify` **`external`** tanımındaki `output { userId: id … }` yüzünden `auth.userId` yazılabilir. Bu bir **modül yereli**dir; `use PrivyAuth` dendiğinde bu blok kök operasyona dahil edilir, dışarıdan görünmeyen bir global değildir.
- Karmaşık akışlarda “her şey context’ten ve explicit çağrılardan gelsin” kuralı: önce kök ihtiyaçlar `input` + gerekirse ilgili **`read` / `create`** içindeki **`rule`** bloğunda **`row = read Platform({ … })`** gibi atamalar; modül sadece tekrar eden yaşam döngüsünü paketler, yeni veri kaynağı eklemiyorsa ekstra sihir yapmaz.

---

# 🧱 Model

**`index` ve `relation` diye ayrı bloklar yok.** İndeks / tek alan benzersizliği, **alan satırındaki niteliklerle** (`--unique`, `--index`, …) verilir.

**Null / optional yok:** tiplerde `?` yok; ilişki alanları da **zorunlu** tek yön. **`many` diye anahtar kelime yok** — birden fazla çocuk satır, **çocuk** modelde üstü gösteren alanla (`user User`, `platform Platform`, …) modellenir; üst modelde “liste alanı” tanımlanmaz.

**Her modelde örtük alanlar** (tekrar yazılmaz): **`id`**, **`createdAt`**, **`status`** (yaşam / dış iş hattı — bkz. aşağıdaki bölüm). **`softDelete`** `config` ile açıksa **`deletedAt`** da örtük eklenir; modele yazılmaz.

| Alan niteliği | Anlam |
| ------------- | ----- |
| `--unique` | bu alanda tekil benzersizlik (soft delete açıkken derleyici çoğu zaman “aktif satırlar” için kısmi benzersizlik üretir) |
| `--index` | sıradan indeks |
| `--index desc` | azalan indeks (ör. listeleme) |

**İlişki:** `user User`, `platform Platform`, **`wallet Wallet`** gibi **alanAdı + model adı** → bu tabloda tutulan FK (sütun adı çoğunlukla `userId`, `platformId` … üretimine çevrilir). Üst–alt 1:N için üstte `many` yok; ör. birçok `Wallet` için yalnızca `Wallet` satırında `user User` ve gerekiyorsa `platform Platform` bulunur. **1:1** (ör. platform–cüzdan) üst modelde `wallet Wallet` ile ifade edilir; diğer uçta `platform Platform` eşlenir.

**Bileşik benzersizlik** (birden fazla alanın birlikte eşsiz olması): model gövdesinde **`unique(a, b, …)`** satırı — ayrı bir `index { }` bloğu değil.

```fsl
model User {
  email string --unique --index
}

model Platform {
  custodyId id
  wallet Wallet
}

model Wallet {
  platform Platform
  user User
  label string
}

model Course {
  code string --unique
}

model CourseEnrollment {
  course Course
  student User
  unique(course, student)
}
```

---

# 📊 Entity `status` ve dış iş orkestrasyonu

**Null / optional ile “yarım satır” yok.** İlişki okumaları (`read … limit 1`) satır bulamazsa istek **başarısız** olur; bu yüzden **`notEmpty(wallet)`** gibi “satır var mı” kontrolleri yazılmaz — zaten tiplenmiş ilişki ve sorgu bunu garanti eder. Üçüncü parti ve outbox ile geciken iş ise **`status`** ile görünür.

## Örtük `status` (tüm entity’ler)

| Değer (örnek) | Anlam |
| --------------- | ----- |
| `initiate` | Satır DB’de; henüz bu satır için tanımlı **dış iş / outbox zinciri tamamlanmadı** veya hiç başlamadı. |
| `progress` | En az bir **`external`** / outbox adımı **yürütüldü**; hâlâ bekleyen hat var. |
| `done` | Bu satır için (ve politika gereği ona bağlı) **planlanan tüm dış çağrılar ve effect’ler** başarıyla tamamlandı. |

Projede sabit enum adları `config` ile netleştirilebilir; mantık aynı kalır.

## Tek DB transaction, çoklu `create`

Örnek: **`create User`** içinde ardışık **`create AuditLog`**, **`create Wallet`**, … — **aynı veritabanı transaction’ı** içinde kalır ve birlikte commit edilir. **`done`** anlamı: bu kök istekle üretilen satır ağacı için **tüm** sync `external`’lar tamamlanmış, **tüm** outbox kayıtları worker’da işlenmiş olmalı; motor kök (ve tanımlı bağlı) satırların **`status`** alanını buna göre **`initiate` → `progress` → `done`** günceller.

## Blok sırası ve bekleyen iş

1. **`role` / `rule` içindeki sync `external`**: **`modify`** ve persist’ten **önce** çalışır; sonuç gelmeden satır yazımına geçilmez.
2. **`modify` içinde kullanılan sync `external`** (varsa): yine yazımdan **önce** bloklayıcı tamamlanır.
3. **`modify` → persist** sonrası **`effect`**: outbox’a düşer; worker asenkron çalışır ve ilgili satır(lar)ın **`status`**’ünü günceller.

Böylece “modify’da external varsa önce o” kuralı, hem **`rule`** hem **`modify`** tarafında deterministik sırada uygulanır; **tam anlamıyla iş bitti** bilgisi **`done`** ile okunur.

---

# ⚙️ Operation (Create)

```fsl
create User {
  role {
  }

  rule {
    isEmail(input.email)
  }

  modify {
    email = lower(input.email)
  }

  effect {
  }
}
```

`create User` için **`input`**, istemciden gelen ve şemaya bağlı **kök oluşturma payload’ıdır**. **`output`**, persist sonrası satırdır — örtük alanlar dahil **`id`**, **`createdAt`**, (`email`, …). `effect` ve iç içe `create` buna referans verir; `createdAt` genelde motor tarafından doldurulur, `modify` içinde tekrar etmek zorunlu değildir.

---

# 📥 `input` ve `output` (kök operasyon)

| İsim | Ne |
| ---- | -- |
| `input` | Bu kök operasyona gelen isteğin veri tipi (create/update payload). |
| `output` | Persist sonrası kök model satırı; yalnızca yazım başarılı olduktan sonra anlamlıdır. |

Örnek: kök işlem `create User` ise `effect` içinde **`output.id`**, iç içe **`create AuditLog({ creator: output.id, ... })`** ile kullanılır.

---

# 🧩 CRUD gövdesi: yalnızca dört blok

**`create` / `read` / `update` / `delete`** altında **başka üst seviye anahtar kelime yok**; yalnızca **`role`**, **`rule`**, **`modify`**, **`effect`**. İçerik gerekmeyen bloklar boş bırakılabilir veya atlanabilir (dil kuralına göre).

---

# 🔐 Rol ve custody kapsamı (örnek)

**Durum:** `CustodyAdmin` rolü şart. `Platform` doğrudan `custodyId` taşır; `Wallet` → `Address` → `Transaction` zincirinde **txn** satırında custody yok — erişim üst ilişkiden gelir. İstek **yalnızca kendi custody’sine** ait platformları ve onun altındaki verileri görmeli.

Örnekte **`context`**, JWT / oturumdan gelir: en azından **`custodyId`** ve **`roles`** (ör. `list` veya sabit enum kümesi); şema projede bir kez tanımlanır.

**Doğrudan custody’si olan kök:** `Platform` listesi veya tekil okuma — filtre basit.

```fsl
read Platform({
  role {
    inRoles(context.roles, "CustodyAdmin")
    notEmpty(context.custodyId)
  }

  rule {
    where {
      custodyId == context.custodyId
    }
  }

  modify {
  }

  effect {
  }
})
```

**Txn gibi altta kalan tablo:** `where` içinde **model ilişkileri üzerinden yürüyerek** (address → wallet → platform) aynı custody’ye sabitle; derleyici bunu `EXISTS` / `JOIN` ile SQL’e çözer — satırda `custodyId` olması gerekmez.

```fsl
read Transaction({
  role {
    inRoles(context.roles, "CustodyAdmin")
    notEmpty(context.custodyId)
  }

  rule {
    where {
      address.wallet.platform.custodyId == context.custodyId
    }

    orderBy createdAt desc
    cursor { size 20 }
  }

  modify {
  }

  effect {
  }
})
```

Özet: **Kimlik / rol** için **`role`**; **filtre ve sorgu şekli** için **`rule`** (içinde `where`, gerekirse `orderBy`, `cursor`). **`read`** için **`modify`** / **`effect`** çoğu zaman boş kalır.

---

# Veri grafiği (`role` / `rule` içinde)

Üst seviye **`bind`** diye bir anahtar kelime yok. **`read`**, **`create`**, **`update`**, **`delete`** gövdesinde atamalar yalnızca **`role`** veya **`rule`** içinde yazılır.

`use` yalnızca **modül dahil etme** içindir (`use PrivyAuth`).

**Çağrı şekli:** `Hedef({ alan: ifade, ... })` — süslü parantez içinde alanlar; isteğe bağlı alan yoktur, her alan zorunlu ve tipe uyar. Dönüş kullanılacaksa **`sonuç = Hedef({ ... })`**.

## Örnek: `rule` içinde okuma

```fsl
create Example {
  role {
  }

  rule {
    user = read User({
      where { id == input.userId }
      limit 1
    })
  }

  modify {
  }

  effect {
  }
}
```

## İç içe `create` (audit log)

Kök `create User` akışında — kök satıra **`output`** ile bağlanır; ifade yine bir **`create`** gövdesinin **`rule`** / **`effect`** içinde olur:

```fsl
log = create AuditLog({
  message: "user created"
  creator: output.id
})
```

## `external` çağrısı

**Tanım şart:** `PrivyVerify({ ... })` için önce **`external PrivyVerify { input {} output {} }`** bildirimi.

```fsl
rule {
  auth = PrivyVerify({
    token: input.token
  })
}
```

---

# 🔥 effect (Async / Outbox)

Kök **`output`** burada kullanılabilir. Dönüş değeri gerekmiyorsa çağrı tek başına; outbox anahtarı vb. meta ayrıca tanımlanır (ör. `key`). **`effect`**, ilgili **`create` / `update`** gövdesinin son blokudur.

```fsl
create User {
  role {
  }

  rule {
  }

  modify {
  }

  effect {
    SendMail({
      title: "Welcome"
      text: concat("Hello ", output.email)
      to: output.email
      auth: env.string("MAIL_AUTH")
    })
  }
}
```

---

# 🌍 external (Sözleşme)

Tüm dış sözleşmeler (senkron worker çağrısı, e-posta, üçüncü parti doğrulama, …) yalnızca **`external`** ile tanımlanır; **`compute` diye ayrı bir anahtar kelime yok.**

Her **`external`** için **`input` ve `output` zorunludur**; isteğe bağlı alan yoktur. Tipler geniş bir kümeden seçilir (`string`, `email`, `id`, `json`, `date`, …). Bloklayıcı çağrılar için `timeout` gibi nitelikler tanımda verilebilir.

```fsl
external SendMail {
  input {
    title: string
    text: string
    to: email
    auth: string
  }

  output {
    messageId: id
    status: string
  }
}

external PrivyVerify {
  input {
    token: string
  }

  output {
    userId: id
    account: json
    wallet: json
    address: json
  }

  timeout 2s
}
```

**Çağrı:** aynı `({ ... })` sözdizimi — dönüş gerekirse atama:

```fsl
mailResponse = SendMail({
  title: "Hi"
  text: "hello"
  to: "x@x.c"
  auth: "xxxx"
})
```

Gerekmezse `SendMail({ ... })` tek satır.

---

## İsteğe bağlı yüzey: “sınıf / metot” gibi gruplama

Tek tek **`JwtVerify`**, **`ValidateJwt`**, **`ParseJwt`** isimleri yerine aynı aileyi **nokta ile** yazmak isteyebilirsin — bu **çalışma zamanında bir class yaratmaz**; sadece okunabilirlik ve isim alanı. Derleyici bunu yine **`external`** sözleşmelerine bağlar.

| Düşünce (sözlük) | Gerçekte |
| ------------------ | -------- |
| `Jwt.verify({ … })` | `JwtVerify` veya `Jwt_verify` sözleşmesi |
| `Jwt.validate({ … })` | `ValidateJwt` |
| `Jwt.parse({ … })` | `ParseJwt` |
| `Fraud.check({ … })` | `FraudCheck` |

**İki uygulama seçeneği (dil / derleyici kararı):**

1. **Saf isim eşlemesi:** `Jwt.verify` → şemada tanımlı tam ad `JwtVerify` (veya `external Jwt { verify { … } }` iç içe sözdizimi — bkz. aşağı).
2. **İç içe `external` bloğu (grup):** aynı dosyada mantıksal gruplama, çağrıda nokta kullanımı:

```fsl
external Jwt {
  verify {
    input { token: string }
    output { userId: id }
  }

  validate {
    input { token: string }
    output { valid: boolean userId: id expiresAt: date issuerOk: boolean }
  }

  parse {
    input { token: string }
    output { userId: id claims: json expiresAt: date issuedAt: date }
  }
}
```

Çağrı: **`Jwt.verify({ token: input.token })`**, **`Jwt.validate({ … })`**, **`Jwt.parse({ … })`** — kafadaki “class method” ile hizalı; arkada yine **input/output sözleşmesi** ve tek bir RPC / worker köprüsü vardır.

Özet: **Class / metot benzetmesi** istiyorsan ya **noktalı çağrı + isim eşlemesi**, ya da **`external Jwt { verify {} validate {} }`** gibi **gruplanmış blok** kullan; ikisi de `Transaction.fql`’deki düz `JwtVerify({…})` ile aynı işi yapabilir — hangisini seçeceğin dokümantasyon + derleyici üretimine kalır.

---

# 🧩 Module (Reusable Lifecycle)

Modül gövdesi de aynı dört blokla çalışır. `auth` **`role`** içinde atanır; kök operasyonda `use PrivyAuth` yoksa bu isim de yoktur.

```fsl
module PrivyAuth {
  role {
    auth = PrivyVerify({
      token: input.token
    })
  }

  rule {
  }

  modify {
    privyUserId = auth.userId
  }

  effect {
    create Account({
      userId: output.id
      data: auth.account
    })

    create Wallet({
      userId: output.id
      data: auth.wallet
    })

    create Address({
      userId: output.id
      data: auth.address
    })
  }
}
```

---

# 🧠 Module Kullanımı

## `use` sadece tek operasyonda

```fsl
create User {
  use PrivyAuth

  role {
  }

  rule {
    isEmail(input.email)
  }

  modify {
    email = lower(input.email)
  }

  effect {
  }
}
```

Burada **`use PrivyAuth`** yalnızca bu **`create User`** için geçerlidir.

---

## `use` model seviyesinde (yaşam döngüsünü birleştirmek)

Aynı modülü **her yüzeyde** tekrar yazmamak için **`model { use VerifyJwt … }`** kullanılabilir. Örnek: JWT ile **`principal`** üretimi — modülün **`role`** bloğu, bu modeldeki **`create` / `update` / …** ile **birleşir**; operasyon tarafında **`role { }` boş** kalabilir (birleşik gövde yine **`principal`**’ı sağlar).

**Birleştirme mantığı (hedef):**

1. **Modül önce**, sonra **operasyon** gövdesi — aynı isimli bloklar **ardışık birleşir**: önce modülün `role`, sonra bu `create`’in `role` (ikisi de çalışır; tipik desende modül `role` dolu, kök `role` boştur).
2. **`rule` / `modify` / `effect`** için de aynı ilke: modül parçası + operasyon parçası **tek mantıksal blok** gibi derlenir.
3. Modülde tanımlı isimler (**`principal`**, modülün `modify` çıktıları vb.) kök operasyonda **görünür**; kök operasyon `use` etmezse yoktur.

Böylece **`use VerifyJwt`** ile kimlik doğrulama yaşam döngüsü **tek modülde** toplanır; iş modeli (`Transaction`) sadece kendi **`rule` / `modify` / `effect`** kurallarını yazar.

**Örnek `VerifyJwt` modülü (katmanlar):**

| Blok | Ne |
| ---- | -- |
| `role` | **`JwtVerify({ token: input.token })` → `principal`** — hızlı doğrulama + `userId`. |
| `rule` | **`ValidateJwt({ token })`** — imza, süre, issuer vb.; **`jwtOk.valid`**, **`jwtOk.issuerOk`**, **`principal.userId == jwtOk.userId`**. |
| `modify` | **`ParseJwt({ token })` → `jwtParsed`** — `claims`, `expiresAt`, … sonraki bloklar / audit için. |
| `effect` | Genelde boş; mail veya revoke burada kalmaz (ayrı politika). |

Böylece **doğrulama** (`ValidateJwt`) ile **çözümleme** (`ParseJwt`) ayrılır; `principal` zaten `role`’da vardır.

---

## Pipeline’da değişkenler (`role` → `rule` → `modify` → `effect`)

Tek bir **`create` / `update` / …** içinde akış **yukarıdan aşağı**dır; **her blokta atanan yerel isimler**, **sonraki bloklara** taşınır (blok içi sıra da önemlidir: önce atama, sonra o isimle ifade).

| Nerede atandı | Sonraki nerelerde kullanılır |
| ------------- | ---------------------------- |
| `role` | `rule`, `modify`, `effect` |
| `rule` | `modify`, `effect` |
| `modify` | `effect` (çoğu senaryoda satır **`output`** üzerinden) |

**`external` dönüşü** de aynı şekilde **`jwtOk = ValidateJwt({ … })`** gibi **bir isme bağlanır**; **`jwtOk.valid`** hem aynı `rule` satırlarında hem sonraki bloklarda kullanılabilir (isim `rule`’da doğduysa `modify`’a kadar).

### `use Modül` birleşince kapsam

1. Her aşamada **önce modülün** ilgili bloğu, **sonra kök operasyonun** bloğu birleştirilir (daha önceki bölüm).
2. Modülün **`role`’unda** üretilen **`principal`**, **`rule`’unda** üretilen **`jwtOk`**, **`modify`’ında** üretilen **`jwtParsed`** gibi isimler, **kök `rule` / `modify` / `effect`** içinde **yerel değişken** gibi erişilebilir — ekstra önek gerekmez, **isim çakışması** olursa derleyici hata verir veya operasyon tarafı önceliklidir (dil kuralına göre tekilleştirilir).

### Örnek: `Fraud` modülü + `check` yanıtı

```fsl
module Fraud {
  rule {
    check = FraudCheck({
      userId: principal.userId
      amount: input.amount
    })

    check.allowed == true
  }

  modify {
  }

  effect {
  }
}
```

`create Transaction { use Fraud … }` içinde **`check`** (ve **`check.score`**), modül **`rule`** birleşimi sayesinde kök **`modify`**’da **`score = check.score`** ile kullanılabilir — **pipeline’da “yerel değişken” tam olarak budur**: önceki blokta atanan isim, sonraki blokta okunur.

### `external` tanımı modül içinde mi, kökte mi?

| Yaklaşım | Açıklama |
| -------- | -------- |
| **Kök şema** | `external FraudCheck { input {} output {} }` — tüm modüller ve operasyonlar görür; araçlar ve arama kolay. |
| **Modül içi (ileri / opsiyonel)** | `module Fraud { external FraudCheck { … } }` — sözleşme modüle **yerel**; derleyici **isim alanını** ayırır (ör. yalnızca `Fraud` kullanan yüzeylerde görünür veya `Fraud.FraudCheck` gibi üretir). Taşınabilir “paket” modül için uygundur. |

İki stil aynı anda kullanılabilir; çakışan isimlerde kök tanım / modül önceliği dil kurallarıyla netleştirilir.

---

## FK alanı `fromWalletId: Wallet` ve `fromWallet`

Alan tipi doğrudan **başka model adı** ise (`fromWalletId: Wallet`), hem **FK** hem tip bilgisidir. **`rule` / `modify` içinde** ilişkili satıra **`fromWallet`** ile (ID’den) erişilir — ayrıca **`read Wallet({ where … })` yazılmaz**; satır yoksa sorgu zaten hata verir.

---

## `rule`’da atanan isimler ve `modify`

Özet: **`check = FraudCheck({ … })`** `rule`’da atanır; **`modify`**’da **`score = check.score`** — ayrıntı için yukarıdaki **Pipeline’da değişkenler** ve **Fraud** örneği.

---

# 🔍 Read (Query DSL)

```fsl
read User {
  role {
  }

  rule {
    where {
      contains(email, "gmail")
    }

    orderBy createdAt desc

    cursor {
      size 20
    }
  }

  modify {
  }

  effect {
  }
}
```

---

# 🔁 Cursor Pagination

```fsl
cursor {
  field createdAt
  direction desc
  size 20
}
```

---

# 🧮 Aggregation

```fsl
return {
  total: sum(transactions.amount)
}
```

---

# 🔐 Lock (Concurrency)

```fsl
create Example {
  role {
  }

  rule {
    wallet = read Wallet({
      where { userId == input.userId }
      lock true
    })
  }

  modify {
  }

  effect {
  }
}
```

---

# 🧠 Yerleşik ifadeler

Hepsi aynı “fonksiyon türü” değildir: biri **satır içi skalar** ifadeye, biri **doğrulama** ifadesine, biri **filtre koşuluna**, biri ise yalnızca **küme üzerinde tek değer** (SQL aggregate) üretmeye derlenir. Yanlış bağlamda kullanılmaz.

## Skalar (`modify`, alan ataması, `rule` içi hesap)

Tek satır / tek değer üzerinde çalışır; SQL’de genelde sütun veya parametre ifadesi olur.

| İfade | Anlam |
| ----- | ----- |
| `lower(x)`, `upper(x)` | metin dönüşümü |
| `trim(x)` | baş/son boşluk |
| `concat(a, b)` | metin birleştirme |
| `length(x)` | uzunluk |
| `now()` | işlem anı (transaction zamanı) |

## Doğrulama (`rule`; sonuç boolean — yetki dahil)

| İfade | Anlam |
| ----- | ----- |
| `isEmail(x)` | e-posta biçimi |
| `notEmpty(x)` | null veya boş değil |
| `inRoles(roles, "RolAdı")` | rol listesinde üyelik |

## Koşul / arama (`where`, filtre; SQL predicate)

İlişki veya sütun üzerinde satır seçimine çevrilir (ör. `LIKE`, karşılaştırma).

| İfade | Anlam |
| ----- | ----- |
| `contains(sütun, alt)` | alt dize eşleşmesi (ör. `LIKE` semantiği) |

## Aggregate (yalnızca küme sorgusu — `return { }` vb.)

**Satır bazlı `modify` veya tek kayıt `read` içinde kullanılmaz**; ilişkili satırlar kümesi üzerinde tek skalar üretir — SQL `SUM`, `COUNT`, … olarak derlenir. Sözdizimi için dokümanda **Aggregation** başlığına bak.

| İfade | Anlam |
| ----- | ----- |
| `sum(expr)` | toplam |
| `count(expr)` / `count(*)` | adet |
| `avg(expr)` | ortalama |
| `min(expr)`, `max(expr)` | minimum / maksimum |

---

# ⚙️ Config (Global)

**Soft delete** yalnızca burada yapılandırılır; başka bir “hard delete” modu yok. Kapalıysa silme anlamı taşıyan operasyonlar tanımsız veya derleme hatası olabilir (dilin tam kuralına göre).

```fsl
config runtime {
  softDelete {
    enabled true
  }

  pagination {
    defaultSize env.int("CURSOR_SIZE", 20)
    maxSize 100
  }

  cursor {
    field createdAt
    direction desc
  }

  timezone "UTC"
}
```

---

# 🔁 Override Önceliği

```
operation > model > config > default
```

---

# 🧨 Scope Kuralları

* `use ModülAdı` yalnızca operasyonda modül kompozisyonu içindir
* `role` / `rule` içinde atanan değişkenler (`read` / `external` dönüşü) block scoped
* modül **`role`** bloğundaki `auth = PrivyVerify({ … })` gibi isimler **o modülün** yerelidir; kök operasyon `use` etmedikçe görünmez
* dışarı sızmaz
* `modify` ile export edilir

---

# 🔥 Engine Mimarisi

```
FSL → AST → SQL Compiler → DB
                    ↓
                 Outbox
                    ↓
                 Worker
```

---

# ⚠️ Kurallar

* ❌ Serbest JS/Go iş mantığı DSL içine gömülmez; veri dönüşümü ve sorgu DSL + SQL ile kalır
* ✅ **`external`** çağrıları **`modify` öncesinde** (çoğunlukla **`role` / `rule`** içinde) kullanılabilir — **latency artar**, bu bir **ürün tercihi**dir
* ✅ Her **`external`** sözleşmesinde **`input` ve `output` tam**; isteğe bağlı alan yok
* ✅ **`Auth({})` / `SendMail({})` gibi çağrılar** yalnızca önce **`external`** ile tanımlanmış isimler için geçerlidir; tanımsız sözleşme kullanımı yoktur
* ✅ Çağrılar **`Ad({ alan: değer })`**; dönüş **`sonuç = Ad({ ... })`**
* ✅ DB tarafı (okuma / yazma / aggregate) **SQL’e compile** edilir; **`external`** çağrıları bildirilmiş RPC / worker olarak çalışır
* ✅ **Async** dış yan etkiler **`effect` + outbox / worker** ile yürür

---

# 🚀 Özet

FSL:

* ORM değildir
* Query language değildir

👉 **Transactional Dataflow Language**

* DB-first execution
* External orchestration
* Deterministic lifecycle

---

```
```
