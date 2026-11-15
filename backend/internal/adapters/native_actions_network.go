package adapters

func nativeNetworkActions() []NativeAction {
	common := []actionField{{1, "result", "number"}, {2, "error", "number"}}
	fields := func(extra ...actionField) []actionField { return append(append([]actionField{}, common...), extra...) }
	network := []NativeActionInput{{"ssid", "SSID", "ascii", 1, 32, 1}, actionNumber("security", "認証方式（1：OPEN、2：WPA1/WPA2、3：WPA2/WPA3）", 2, 1, 3)}
	makeAction := func(id, group, label, description string, ro bool, input []NativeActionInput, response []actionField) NativeAction {
		a := NativeAction{ID: id, Group: group, Label: label, Description: description, Owner: "Communication", ReadOnly: ro, Inputs: input, Response: response, Result: 1, Success: 1}
		if !ro {
			a.Confirmation = label
		}
		return a
	}
	empty := []NativeActionInput{}
	return []NativeAction{
		makeAction("wifi-status", "純正Wi-Fi接続", "純正Wi-Fiの状態を取得", "本体側のWi-Fi STAの状態を取得します。Windowsの接続は変更しません。", true, empty, fields(actionField{3, "setting", "number"}, actionField{4, "mac", "string"}, actionField{5, "ipv4", "string"}, actionField{6, "savedLimit", "number"})),
		makeAction("wifi-switch", "純正Wi-Fi接続", "純正Wi-FiのON/OFFを変更", "1：ON、2：OFF。Wi-Fi経由の管理接続が切れることがあります。通常の接続設定はWi-Fi画面を使用できます。", false, []NativeActionInput{actionNumber("setting", "Wi-Fi", 1, 1, 2)}, fields(actionField{3, "setting", "number"})),
		makeAction("wifi-profile", "純正Wi-Fi接続", "保存したWi-Fiの詳細を取得", "SSIDと認証方式を指定します。パスワードを含む情報を画面に表示します。", true, network, fields(actionField{3, "ssid", "string"}, actionField{4, "priority", "number"}, actionField{5, "autoConnect", "number"}, actionField{6, "security", "number"}, actionField{7, "password", "string"}, actionField{8, "strength", "number"})),
		makeAction("wifi-saved", "純正Wi-Fi接続", "純正の保存済みWi-Fi一覧を取得", "ページは1始まりです。1ページずつ取得します。", true, []NativeActionInput{actionNumber("page", "ページ", 1, 1, 100)}, fields(actionField{3, "count", "number"}, actionField{4, "total", "number"}, actionField{5, "networks", "saved-ap-list"})),
		makeAction("wifi-nearby", "純正Wi-Fi接続", "純正の周辺Wi-Fi一覧を取得", "先に純正Wi-FiをONにしてください。ページは1始まりで、最大5件ずつ取得します。", true, []NativeActionInput{actionNumber("page", "ページ", 1, 1, 100)}, fields(actionField{3, "count", "number"}, actionField{4, "total", "number"}, actionField{5, "networks", "nearby-ap-list"})),
		makeAction("wifi-connecting", "純正Wi-Fi接続", "接続処理中のWi-Fiを取得", "本体が接続を試みているSSIDを取得します。", true, empty, fields(actionField{3, "connecting", "bool"}, actionField{4, "ssid", "string"})),
		makeAction("wifi-delete", "純正Wi-Fi接続", "純正の保存済みWi-Fiを削除", "指定SSIDと認証方式の接続情報を純正の保存先から削除します。", false, network, common),
		makeAction("wifi-autoconnect", "純正Wi-Fi接続", "純正Wi-Fiの自動接続を変更", "1：ON、2：OFF。指定した保存済みWi-Fiの自動接続を変更します。", false, append(append([]NativeActionInput{}, network...), actionNumber("autoConnect", "自動接続", 3, 1, 2)), common),
		makeAction("wifi-connect", "純正Wi-Fi接続", "純正機能でWi-Fiに接続", "Wi-Fi STAをONにしてから実行してください。管理接続が切れる可能性があります。応答後も実際の接続確認が必要です。", false, append(append([]NativeActionInput{}, network...), NativeActionInput{"password", "パスワード（OPENでは空欄）", "optional-secret", 0, 128, 3}), common),
		makeAction("wifi-connect-saved", "純正Wi-Fi接続", "純正の保存済みWi-Fiへ接続", "純正の保存済み接続情報を使用します。管理接続が切れる可能性があります。", false, network, common),
		makeAction("wifi-disconnect", "純正Wi-Fi接続", "純正Wi-Fiを切断", "Wi-Fi経由の通信が切断されます。USBやTailscaleなどの管理経路を確保してください。", false, empty, common),
		makeAction("wifi-reset", "純正Wi-Fi接続", "純正Wi-Fiの設定を初期化", "純正の保存済みWi-Fi情報を消去します。再設定用のSSIDとパスワード、別の管理経路を確保してください。", false, empty, common),
		makeAction("local-ap-status", "純正ローカルAP", "純正APの状態を取得", "NCCホットスポットとは別の、純正ローカルAPの状態と認証情報です。", true, empty, fields(actionField{3, "setting", "number"}, actionField{4, "ssid", "string"}, actionField{5, "password", "string"}, actionField{6, "security", "number"}, actionField{7, "band", "number"}, actionField{8, "clients", "number"})),
		makeAction("local-ap-switch", "純正ローカルAP", "純正APを開始・停止", "1：開始、2：停止。NCCホットスポットと無線を共用します。接続が切れる可能性があります。", false, []NativeActionInput{actionNumber("setting", "純正AP", 1, 1, 2)}, fields(actionField{3, "setting", "number"})),
		makeAction("local-ap-vehicle", "純正ローカルAP", "純正APの車内判定を取得", "純正APが返す車内判定を確認します。", true, empty, fields(actionField{3, "inVehicle", "number"})),
		makeAction("local-ap-support", "純正ローカルAP", "純正APの対応方式を取得", "対応する認証方式と周波数帯を確認します。", true, empty, fields(actionField{3, "security", "number"}, actionField{4, "band", "number"})),
		makeAction("local-ap-settings", "純正ローカルAP", "純正APの接続設定を変更", "認証方式 1：OPEN、2：WPA1/WPA2、3：WPA2/WPA3。周波数帯 1：2.4 GHz、2：5 GHz。再接続が必要です。", false, []NativeActionInput{{"ssid", "SSID", "ascii", 1, 32, 1}, {"password", "パスワード（OPENでは空欄）", "optional-secret", 0, 128, 2}, actionNumber("security", "認証方式", 3, 1, 3), actionNumber("band", "周波数帯", 4, 1, 2)}, fields(actionField{3, "ssid", "string"}, actionField{4, "password", "string"}, actionField{5, "security", "number"}, actionField{6, "band", "number"})),
		makeAction("local-ap-reset", "純正ローカルAP", "純正APの設定を初期化", "純正APの設定を初期状態に戻します。NCCホットスポットとの無線の共有や再接続に注意してください。", false, empty, common),
	}
}
