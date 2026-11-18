# NP1へのADB初回導入（2026-11-18）

- ADBで認証済みのAArch64機体を対象に、`d4d057b` のControl Centerを初回導入した。ファームウェア `202404231204`、カーネル `4.14.206-perf`、root ADB、既設の所有者用復旧サービスを確認してから配備した。
- 変更計画は `/overlay/owner-apps/np1-control` とControl Center専用のsystemd定義を追加し、既設の復旧サービスの `Wants` に登録するもの。Windows側は既存の80 / 443番の利用を維持し、ADBで8080 → 80、8443 → 443を転送した。
- 最初の起動は端末のsystemdが未登録の数値UID/GIDを解決できず、自動ロールバックした。再試行では既存のPythonを使う起動処理で数値UID/GIDに降格し、Web実行時のケーパビリティを `CAP_NET_BIND_SERVICE` に限定した。rootワーカーの共有ソケットには専用グループを割り当てた。
- 復旧記録は本体の `/overlay/owner-apps/np1-control/recovery` にある。`python tools/deploy.py --serial SERIAL --action rollback-owner-recovery` で導入を取り消し、専用ファイルと設定を時刻付きディレクトリに保存できる。起動時の依存登録は永続領域とsystemdの両方から読み戻した。実機の再起動試験は未実施。
- 配備用manifest 335件を本体上で照合し、ビルド成果物・本体の永続バイナリ・実行中のバイナリのSHA-256が一致した。Web、broker、modem、native、plateが稼働し、専用firewallの適用とナンバー認識センサーのソケットを確認した。
- HTTP / HTTPSでパスワードなしのセッションとWeb画面を確認した。HTTPSの証明書指紋はADB取得値と一致した。前方・車内カメラのJPEG、SD録画のHTTP 206読み出し、ストレージのパス検証、バックアップ出力の資格情報除外を確認した。
- 純正Communication、DriveRec、ImageRecog、Voice、QMMFの稼働と、導入前後のPID・InvocationIDの一致を確認した。起動スクリプト、アカウントファイル、所有者用復旧unitのSHA-256が一致し、OTA停止フラグは導入前と同じく存在しなかった。新規AP・LED・回線自動切替は設定待ちの状態で導入した。
- フロントエンドの型検査と本番ビルド、Goの全パッケージのtest/vet、postbootテスト143件が通った。
- 実機のPlaywrightではデスクトップ・モバイルの管理画面、ライブ表示、初期状態のナンバー認識タブが通った。既存の3ケースは、登録済みのナンバー条件、ホットスポット優先の選択肢、録画時刻入力後の自動再生を期待する箇所で止まった。今回の初期状態では登録済みの条件が0件で、回線設定画面に自動・Wi-Fi・4Gの選択肢が表示された。追加の確認では、前方・車内とも時刻指定、再生操作後の映像デコード、再生位置の進行を確認した。
