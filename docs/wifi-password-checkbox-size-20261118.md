# Wi-Fi / APパスワード表示チェックボックス修正（2026-11-18）

## 変更

- Wi-Fi新規接続フォームの「パスワードを表示する」に、通常の入力欄向け `width: 100%` と `min-height: 44px` が適用されていたため、チェックボックスが入力欄相当の大きさになっていた。
- Wi-Fi新規接続、NCC保存済み接続先、ホットスポット設定のパスワード表示チェックボックスを18×18pxへ統一した。
- ラベル自体は44px以上の操作領域を維持し、チェックボックスだけを小さくした。

## 検証

- Vue型検査と本番ビルドに合格した。
- Wi-Fi / AP合成Playwright試験11件に合格した。
- 320pxと390pxのモバイル表示で、Wi-Fi新規接続とAP設定のチェックボックスが18×18pxであり、横方向へあふれないことを確認した。
- 320pxのlight表示を画像で確認し、チェックボックスとラベルの位置・操作領域が他画面と同等であることを確認した。
- Go全パッケージのtest/vet、postboot単体試験143件、arm64成果物と331ファイルの配布マニフェスト生成に合格した。

## 実機導入

- コミット `5dc1b28` を利用者指定のTailscale接続先へ `tools/device/web-worker-update.sh` で導入した。接続先IPや端末識別子は公開記録へ含めない。
- 配布物、永続バイナリ、実行中バイナリのSHA-256は `d335e10ab7f6136115fbf86b079033ca34823e237de41bf3f8318224b613e0f8` で一致した。
- Wi-Fi接続先、ホットスポット、NumberShareの保存設定は更新前後でSHA-256が一致した。Windows側と実機側のWi-Fi/AP設定操作は行っていない。
- 復旧スナップショットは `/overlay/owner-apps/np1-control/recovery/web-worker-20261117T171737Z`。
- Web、broker、AP、modem、native、plateと、純正Communication、DriveRec、ImageRecog、Voice、QMMFは `active/running`、`Result=success`、`NRestarts=0`。純正5サービスは更新前後のPID・InvocationIDが一致した。
- HTTPS 200、パスワードなしの所有者セッション、UIシェルを確認した。転送用HTTPサーバーと端末側の一時配布物は削除した。
