# pare

[English](README.md)

**AI コーディングエージェントのための、コンテキスト予算を意識した出力切り詰め。**
pare は stdin を読み、バイト予算に収まるように切り詰めた内容を stdout に書き出す。
先頭（**head**）・末尾（**tail**）・**エラー行**（前後の文脈つき）を残す——
つまり、素朴な `| tail` が捨ててしまう「中盤」を保持する。

```
your-command 2>&1 | pare
```

## なぜ

エージェント（や人間）は、コンテキストを溢れさせないよう、ノイズの多いコマンドを
防御的に `| tail` へ通す。だが素朴な tail は出力の *中盤* に出たエラーを落とすので、
本当に必要な 1 行が消え、コマンドの再実行という二度手間になる。pare は head・tail・
**エラー行** を固定バイト予算の中に収めるので、失敗が 1 パスで見える。

```
noise 1
noise 2
noise 3
[... 395 lines omitted ...]
noise 399
noise 400
ERROR: undefined symbol _foo at link time      ← 素朴な `| tail` はこれを落とす
noise 401
noise 402
[... 395 lines omitted ...]
noise 798
noise 799
noise 800
```

## インストール

```sh
# Homebrew (macOS/Linux)
brew install akira-toriyama/tap/pare

# Go
go install github.com/akira-toriyama/pare/cmd/pare@latest

# Nix（ソースビルド。version は "dev" 表示）
nix run github:akira-toriyama/pare
```

各リリースのビルド済みバイナリと checksum は
[Releases](https://github.com/akira-toriyama/pare/releases) にある。

## 使い方

```sh
# 既定: 予算 8 KiB / head 15 / tail 15 / context 2 / 組み込みエラーパターン
some-build 2>&1 | pare

# 予算を絞り、フル出力を保存し、マッチャを追加
make 2>&1 | pare --budget-bytes 4096 --tee /tmp/build.log --match WARN

# 上流の exit code をシェルに見せる
set -o pipefail; go test ./... 2>&1 | pare

# テスト実行: 失敗したアサーションのブロックを丸ごと残し、成功は畳む
go test -v ./... 2>&1 | pare --profile test
swift test 2>&1 | pare --profile test
```

### フラグ

| フラグ | 既定 | 意味 |
|---|---|---|
| `--budget-bytes` | `8192` | 出力のバイト上限（`0` で予算を無効化＝全文素通し）。 |
| `--head` | `15` | 先頭から必ず残す行数。 |
| `--tail` | `15` | 末尾から必ず残す行数。 |
| `--context` | `2` | マッチ行の前後に残す文脈行数。 |
| `--match` | 組み込み | エラー行の正規表現（[RE2](https://github.com/google/re2/wiki/Syntax)）。繰り返し可・指定時は既定を **置換**。 |
| `--profile` | – | 抽出プロファイル。`test` はテストランナーの失敗に合わせ、インデントされたアサーションのブロックを丸ごと残す。空 = 汎用。 |
| `--tee FILE` | – | フル（未切り詰め）入力を `FILE` に書き、省略マーカーに参照を記す。 |

組み込みマッチャ（大文字小文字を無視）:

```
\b(error|fail(ed|ure)?|exception|fatal|panic|abort|denied|traceback|undefined symbol|cannot find|assert)\b
```

`--match` を 1 つ以上渡すと既定を上書きする（例: `--match 'WARN|deprecated'`）。

### `test` プロファイル

`--profile test` はテストランナーの出力を流すためのもの。2 点が変わる:

- **汎用のエラー語 regex ではなく構造的な失敗アンカー** で拾う——`--- FAIL:` /
  `FAIL` / `panic:`（Go）、`: error:`（Swift の XCTest / clang）、失敗マーク
  `✘` `✗` `●` `✕` `×`（Swift Testing / jest / vitest）、`FAILED` / `E `（pytest）、
  `file:line:col:` のビルド診断。だから通常のログ本文や成功行にはマッチしない。
- **固定の `--context` 幅ではなくアサーションのブロック全体** を残す——アンカーに
  マッチしたら、その周囲の連続したインデント本体（`Error Trace` / `expected` /
  `actual` などの got/want 詳細）を、ヘッダの下（`go test`）でも上（`go test -v`）でも
  丸ごと保持する。成功したテストは `[... N lines omitted ...]` に畳まれる。

それ以外は不変: pare は今も **行を選ぶだけ**（要約・集計・JSON 出力はしない）で、
`--budget-bytes` / `--tee` も従来どおり、上流の exit code も読まない。明示的な
`--match` はマッチャとして優先され、プロファイルのブロック挙動はどちらでも効く。

```sh
go test -v ./... 2>&1 | pare --profile test
swift test  2>&1 | pare --profile test
pnpm test   2>&1 | pare --profile test
```

### パイプで知っておくべき 2 点

- **stderr を混ぜる。** 多くのエラーは *stderr* に出るので `2>&1 |` で pare に流す。
  さもないと pare は stdout しか見えない。
- **pare は上流の exit code を見られない。** pare はフィルタなので、その exit status は
  *pare 自身* のもので、流し込むコマンドのものではない。上流の結果が重要なら
  `set -o pipefail` を使い、上流の非ゼロ終了でシェルが失敗するようにする。

## 終了コード

| コード | 意味 |
|---|---|
| `0` | OK — pare は動作した（上流コマンドの成否とは無関係。上記参照）。 |
| `2` | usage / バリデーションエラー（不正なフラグ・不正な `--match` 正規表現）。 |
| `3` | 内部 / I/O エラー（stdin 読み取り不可・`--tee` 書き込み不可）。 |

エラーは **stderr** に出るので、下流の `| jq` や `| grep`（stdout）は汚れない。

## 仕組み

pare は head/tail を先取りし、エラーブロックを古い順に追加し、予算超過時は
context を縮小 → エラーブロックを後方から破棄 → head/tail を床まで縮小、の順で削る。
全ポリシーは [docs/algorithm.md](docs/algorithm.md)、意図的な制限は
[docs/non-goals.md](docs/non-goals.md) にある。

## 開発

```sh
sh scripts/check.sh        # build / vet / test -race / lint / smoke
git config core.hooksPath scripts/hooks   # commit-msg 規約フックを有効化
```

コミットは [gitmoji + Conventional Commits](https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md) に従う。

## ライセンス

[MIT](LICENSE)
