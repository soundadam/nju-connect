import Foundation

func formatBytes(_ bytes: UInt64) -> String {
    ByteCountFormatter.string(
        fromByteCount: Int64(min(bytes, UInt64(Int64.max))),
        countStyle: .file
    )
}

func formatRate(_ bytesPerSecond: Double) -> String {
    let bounded = max(0, min(bytesPerSecond, Double(Int64.max)))
    return ByteCountFormatter.string(fromByteCount: Int64(bounded), countStyle: .file) + "/s"
}
