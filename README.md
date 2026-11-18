# YaagwaDB テスト

YaagwaDB の開発環境・テストと、YaagwaDB を使用した WordPress の Docker コンテナ群。

- [開発環境とテスト](docs/ja/development.md)
- [Development environment and tests](docs/en/development.md)

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

- `../file-db` に YaagwaDB のソースコード
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
