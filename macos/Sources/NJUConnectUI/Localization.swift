import Foundation

enum UILanguage: String {
    case english = "en"
    case simplifiedChinese = "zh-Hans"

    static let current: UILanguage = {
        let value = ProcessInfo.processInfo.environment["NJU_CONNECT_UI_LANGUAGE"]
        return UILanguage(rawValue: value ?? "") ?? .english
    }()
}

/// Keeps English and Simplified Chinese copy together while the UI is evolving.
/// English is the default; set NJU_CONNECT_UI_LANGUAGE=zh-Hans to preview Chinese.
func uiText(_ english: String, _ simplifiedChinese: String) -> String {
    switch UILanguage.current {
    case .english: return english
    case .simplifiedChinese: return simplifiedChinese
    }
}
