# YaagwaDB Test

Development environment and tests for YaagwaDB, and Docker containers for WordPress using YaagwaDB.

- [Development environment and tests](docs/en/development.md)

## WordPress

`wp-content/uploads` is a YaagwaDB FUSE mount, backed up to MinIO.

### Components

- `wordpress`
  - WordPress (Apache) and the YaagwaDB daemon and mount
  - The daemon, mount, and Apache run as www-data
- `db`
  - MariaDB
- `minio`
  - YaagwaDB backup destination (bucket `yaagwadb-wp`)

### Prerequisites

- YaagwaDB source code in `../yaagwadb`
- Docker Compose (with `additional_contexts` support)
- [Task](https://taskfile.dev/)

### Start

```sh
task up
```

- WordPress
  - <http://localhost:8000>
- MinIO console
  - <http://localhost:9011> (user `yaagwadb`, password `yaagwadb-secret`)

### Stop

```sh
task down
```

### Tasks

- `task up`
  - Build and start
- `task down`
  - Stop
- `task clean`
  - Stop and remove all volumes
- `task logs [-- <service>]`
  - Show logs
- `task status`
  - Show the YaagwaDB file count and task queue status
- `task tasks [-- <options>]`
  - Show YaagwaDB tasks
- `task list`
  - List files in uploads
- `task shell`
  - Start a www-data shell in uploads in the wordpress container
- `task checksum`
  - Show SHA-256 of all files in uploads
- `task recovery:wipe`
  - Delete YaagwaDB local data and start (restore from MinIO)
- `task test:recovery`
  - Recovery test. Checks that the SHA-256 of uploads matches before and after `recovery:wipe`

---

# YaagwaDB テスト

YaagwaDB の開発環境・テストと、YaagwaDB を使用した WordPress の Docker コンテナ群。

- [開発環境とテスト](docs/ja/development.md)

## WordPress

`wp-content/uploads` を YaagwaDB の FUSE マウントとし、MinIO にバックアップする。

### 構成

- `wordpress`
  - WordPress（Apache）と YaagwaDB の daemon・mount
  - daemon・mount・Apache は www-data で実行
- `db`
  - MariaDB
- `minio`
  - YaagwaDB のバックアップ先（バケット `yaagwadb-wp`）

### 前提

- `../yaagwadb` に YaagwaDB のソースコード
- Docker Compose（`additional_contexts` 対応版）
- [Task](https://taskfile.dev/)

### 起動

```sh
task up
```

- WordPress
  - <http://localhost:8000>
- MinIO 管理画面
  - <http://localhost:9011>（ユーザー `yaagwadb`、パスワード `yaagwadb-secret`）

### 停止

```sh
task down
```

### タスク

- `task up`
  - ビルドして起動
- `task down`
  - 停止
- `task clean`
  - 停止し、全ボリュームを削除
- `task logs [-- <サービス名>]`
  - ログを表示
- `task status`
  - YaagwaDB のファイル件数とタスクキューの状態を表示
- `task tasks [-- <オプション>]`
  - YaagwaDB のタスクを表示
- `task list`
  - uploads のファイル一覧を表示
- `task shell`
  - wordpress コンテナで www-data のシェルを uploads で起動
- `task checksum`
  - uploads の全ファイルの SHA-256 を表示
- `task recovery:wipe`
  - YaagwaDB のローカルデータを削除して起動（MinIO からリストア）
- `task test:recovery`
  - リカバリーテスト。`recovery:wipe` の前後で uploads の SHA-256 が一致するか確認
