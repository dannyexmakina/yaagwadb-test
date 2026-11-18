# 開発

開発用のタスクは [Task](https://taskfile.dev/) と Docker を使用
`develop/` 内で実行する。

## 開発環境

daemon・hrana・マウントは Linux コンテナ内で実行する。オブジェクトストレージには、MinIO と fake-gcs-server をそれぞれ別のコンテナで実行する。

```sh
cd develop
task run         # MinIO（task run:minio と同じ）
task run:fakegcs # fake-gcs-server
task run:hrana   # MinIO。マウントせず daemon と hrana のみ
```

- hrana はホストの `http://127.0.0.1:8080` で接続可能

Ctrl-C で daemon・hrana・マウントが停止する。

- yaagwadb のバイナリは、ホストで Linux 向けに `../yaagwadb/build/linux/yaagwadb` へクロスビルドし（`task build:linux`）、コンテナから使用。
- ソースコードか埋め込みファイル（assets）が変更された場合のみ再ビルド
- コンテナを起動すると、daemon と hrana を起動してマウント

## テスト

```sh
task test:vet            # go vet
task test:unit           # 単体・crash 注入・リストアのテスト（コンテナ不要）
task test:fuse           # FUSE の結合テスト（Linux コンテナで実行）
task test:object-storage # MinIO・fake-gcs-server との結合テスト
task test                # 上記のすべて
```

## 開発環境での検証

`task run` で起動した daemon・hrana・マウントに対して実行する。

### SQL フロント（hrana）

```sh
task check:hrana                  # INSERT・SELECT・UPDATE・DELETE と、拒否される文を確認
STORAGE=fake-gcs task check:hrana # task run:fakegcs で起動した場合
```

- `check:hrana` はルートに `hrana-<日時>.txt` を書き、最後に論理削除する
- 確認に失敗した場合は終了コード 1

### 負荷テスト

読み込みと書き込みを並行して実行する。

```sh
task check:load                                                 # gRPC 経由。既定値（書き込み 4・読み込み 16 並列、100 ファイル、64 KiB、30 秒）
task check:load -- -duration 1m -size 1048576                   # オプションは -- の後に指定
task check:load:mount                                           # マウント経由
task check:load:stress                                          # 上限を超える負荷（書き込みを 4 から 1024 並列まで倍々、書き込み 512 件を開いたまま保持、1 段階 10 秒）
task check:load:stress -- -mode mount -mountpoint /mnt/yaagwadb # 上限を超える負荷をマウント経由で
STORAGE=fake-gcs task check:load                                # task run:fakegcs で起動した場合
```

- ディレクトリ `load-<日時>` を作り、その中にファイルを書いてから計測を開始する
- 書き込みは既存のファイルをランダムに上書き、読み込みはランダムに読み込む
- 読み込んだファイルデータは、先頭の SHA-256 と残りの部分が一致するか確認する。一致しない場合はエラー
- 段階ごとに、件数・エラー数・ops/s・MiB/s・レイテンシ（p50・p90・p99・max）と、エラーの種類ごとの件数を表示
- 終了後にファイルとディレクトリを削除する（論理削除）。`-keep` で残す

#### 上限を超えるテスト

- `-ramp-max`：書き込みの並列数を `-writers` から倍々に増やし、エラーが出た段階で止める
- `-hold`：書き込みを開始したまま終わらせない件数。計測中は開いたまま保持し、計測後に終わらせる
  - gRPC：target だけを送って止める。daemon は一時ファイルを開いたまま待つ。終わらせるときはストリームを中断（ファイルは登録されない）
  - マウント：ファイルを作って 1 バイト書き、開いたまま。終わらせるときは close（ファイルが登録される）
- 終了後の検証（常に実行）
  - daemon が応答するか（Status）
  - ディレクトリに想定外のエントリが無いか、ファイルが欠けていないか
  - 全てのファイルを読み込み、チェックサムが一致するか
  - 新しい書き込みと読み込みができるか
  - 検証に失敗した場合は終了コード 1

daemon の上限を下げて起動すると、少ない負荷で上限を超えられる。

```sh
task run:limited            # daemon の開けるファイル数を 256 にして起動（MinIO）
NOFILE=128 task run:limited # 数を指定
task check:load:stress      # 別のターミナルで実行
```

- 開けるファイル数は、書き込み中・読み込み中の件数だけ消費される
- スレッド数の上限は、コンテナ内では root で実行するため下げられない

#### オプション

| オプション | 既定値 | 説明 |
| --- | --- | --- |
| `-c` | `/etc/yaagwadb/config.toml` | `socket` を読む設定ファイル |
| `-socket` | （設定ファイルの値） | daemon の socket。指定した場合は `-c` を無視。マウント経由でも、準備と検証に使用 |
| `-mode` | `grpc` | `grpc` か `mount` |
| `-mountpoint` | | マウントポイント（`-mode mount` で必須） |
| `-writers` | `4` | 書き込みの並列数（`-ramp-max` を指定した場合は最初の段階の数） |
| `-ramp-max` | `0` | 書き込みの並列数を倍々に増やす上限。`0` の場合は 1 段階のみ |
| `-readers` | `16` | 読み込みの並列数 |
| `-files` | `100` | ファイル数 |
| `-size` | `65536` | 1 ファイルのバイト数 |
| `-duration` | `30s` | 1 段階の計測時間 |
| `-hold` | `0` | 開いたまま保持する書き込みの件数 |
| `-keep` | `false` | 書いたファイルを残す |

コンテナの外で実行する場合は `go run ./load -socket <socket のパス>`。

#### 計測結果

- 2026-11-15
- `task check:load:mount`（MinIO、書き込み 4・読み込み 16 並列、64 KiB、30 秒）

| ファイル数 | 操作 | 件数 | エラー | ops/s | MiB/s | p50 | p90 | p99 | max |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1000 | 書き込み | 910 | 0 | 30.3 | 1.89 | 99.4ms | 246.5ms | 454.8ms | 765.3ms |
| 1000 | 読み込み | 85398 | 0 | 2839.8 | 177.49 | 5.2ms | 8.8ms | 13.3ms | 41.6ms |
| 10000 | 書き込み | 979 | 0 | 32.5 | 2.03 | 95.8ms | 226.3ms | 407.1ms | 507.8ms |
| 10000 | 読み込み | 78663 | 0 | 2612.0 | 163.25 | 5.6ms | 9.5ms | 14.6ms | 36.6ms |

- 検証は全て成功

### ベンチマーク

1 ファイルずつ書き込みと読み込みを行い、素のファイルシステムとの差を表示する。

```sh
task check:bench                                   # 既定値（4 KiB・64 KiB・1 MiB・16 MiB、各 30 回）
task check:bench -- -count 100 -sizes 4096,1048576 # オプションは -- の後に指定
STORAGE=fake-gcs task check:bench                  # task run:fakegcs で起動した場合
```

- 比較する対象
  - `plain`：素のファイルシステム。書き込みごとに fsync する（daemon も書き込みごとに fsync するため、これを基準にする）
  - `plain-nosync`：素のファイルシステム。fsync しない
  - `grpc`：gRPC 経由（書き込みは Write、読み込みは Lookup と Read）
  - `mount`：マウント経由（`-mountpoint` を指定した場合のみ）
- サイズと操作ごとに、レイテンシ（p50・p99・max）・MiB/s・`plain` との比（p50 同士）を表示
- 並列にはせず、1 件ずつ実行する。負荷時の性能は負荷テストで見る
- `plain` の読み込みはページキャッシュから返ることがある
- `task check:bench` の素のファイルシステムは、コンテナの `/tmp`
- 終了後に書いたファイルを削除する。`-keep` で残す

#### オプション

| オプション | 既定値 | 説明 |
| --- | --- | --- |
| `-c` | `/etc/yaagwadb/config.toml` | `socket` を読む設定ファイル |
| `-socket` | （設定ファイルの値） | daemon の socket。指定した場合は `-c` を無視 |
| `-mountpoint` | | マウントポイント。空の場合はマウント経由を測らない |
| `-plain` | OS の一時ディレクトリ | 比較する素のファイルシステムのディレクトリ |
| `-sizes` | `4096,65536,1048576,16777216` | ファイルサイズ（バイト、カンマ区切り） |
| `-count` | `30` | サイズ・対象・操作ごとの回数 |
| `-keep` | `false` | 書いたファイルを残す |

コンテナの外で実行する場合は `go run ./bench -socket <socket のパス> -mountpoint <マウントポイント>`。

## 開発環境の操作

### ファイルの操作

マウント先（`/mnt/yaagwadb`）はコンテナの中に設置される。
`docker cp` はマウントを通らないため、次のタスクを使用する。

```sh
task dev:put -- <ローカルファイル> # 同じ名前でマウント先に書き込む
task dev:list              # マウント先のファイル一覧（ドットファイルを含む）
task dev:delete -- <名前>    # マウント先から削除する（ローカルのパスでも可。名前のみを使用）
task dev:shell             # マウント先でシェルを起動
```

`status` などのコマンドの実行例：

```sh
docker exec yaagwadb-develop /src/build/linux/yaagwadb -c /test/develop/configs/develop-minio.toml status
```

### SQL の実行

`task run` で起動した hrana で 1 文を実行し、応答を表示する。

```sh
task dev:sql -- 'SELECT path, size FROM objects' # 1 文を実行して応答を表示
```

### オブジェクトストレージの内容確認

```sh
task dev:list:minio   # MinIO のバケット内オブジェクト
task dev:list:fakegcs # fake-gcs-server のバケット内オブジェクト
```

開発用のバケットは `yaagwadb-develop`。MinIO の管理画面は <http://localhost:9001>（ユーザー `yaagwadb`、パスワード `yaagwadb-secret`）。
