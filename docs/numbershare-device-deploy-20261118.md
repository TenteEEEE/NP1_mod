# NumberShare 管理閲覧修正の実機導入（2026-11-18）

## 対象と導入方法

- 利用者が指定したTailscale接続先へ `feat/number-share-api` のWeb/API内包バイナリを導入した。接続先IP、端末識別子、認証情報はこの公開記録へ含めない。
- 対象の5555番はADBではなく既存のroot管理コンソールだったため、Windows側のWi-Fi設定を変更せず、Tailscale内の一時HTTPサーバーからハッシュ固定アーカイブを端末の `/tmp` へ転送した。
- 永続領域の変更は既存の `tools/device/web-worker-update.sh` だけで行った。旧バイナリは `/overlay/owner-apps/np1-control/recovery/web-worker-20261117T151618Z` へ保存され、失敗時の自動復元を有効にした。
- 最終導入コミットは `8cc7345`。リモートの `origin/feat/number-share-api` へpush済みである。

## 実APIで判明した修正

- mainから移行済みの機器用キーは有効で、`users/me` の接続確認はHTTP 200だった。初回登録は再実行していない。
- 引継ぎ資料の管理画面用キーを外部APIで検証し、既存の機器用キーを変更せず、管理画面URLと管理キーだけを端末の0600設定へ保存した。
- 初回導入時、全検知履歴と登録車両検索が機器用キーを使用し、外部APIからHTTP 403「管理権限が必要です」を受けた。管理閲覧だけ保存済み管理キーを優先し、検知送信と `users/me` は引き続き機器用キーを使うよう修正した。
- 修正版で全検知履歴はHTTP 200、100件（自分95件、他利用者5件）、`truncated=false` で全ページを受信した。登録車両検索もHTTP 200で、この時点の登録件数は0件だった。
- 送信キューは送信済み95件、送信待ち0件、確認が必要0件。リアルタイム送信は既存設定どおり停止状態を維持し、新たな送信は行っていない。

## 検証結果

- `tools/build.ps1 -SkipInstall` により、Vue型検査・本番ビルド、Go全パッケージのtest/vet、Linux arm64成果物、依存ライセンス、331ファイルの導入manifestを検証した。
- postboot単体テストは143件合格した。管理閲覧が管理キーを使い、機器認証確認が機器用キーを維持する回帰テストを追加した。
- 最終成果物、永続バイナリ、実行中バイナリのSHA-256は `fe2bb0ffff7f0d18479578cdcde317d78137e8df1e2bffbaa1c54dcbd81ad998` で一致した。
- Web、broker、AP、modem、native、plateは `active/running`、`Result=success`、`NRestarts=0`。AP・modem・native・plateのPIDは更新前後で同一だった。
- 純正Communication、DriveRec、ImageRecog、Voice、QMMFは更新前後でPID・InvocationIDが同一で、`active/running`、`Result=success`、`NRestarts=0` だった。
- TLS証明書は端末側のDER指紋と既存の信頼記録が一致した。前方・車内カメラは完全なJPEGを取得し、テスト用セッションを削除した。バックアップ出力に `passwordHash` が含まれないことを確認した。
- この時点ではSDカードが未マウントで、固定録画パスのMP4 Range検証だけは `NO_SD` のため実行できなかった。NumberShare、カメラ、純正サービスの検証結果には影響しない。
