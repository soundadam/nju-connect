import Foundation

func formatBytes(_ bytes: UInt64) -> String {
    // ByteCountFormatter spells small counts out ("894 bytes").
    if bytes < 1_000 {
        return "\(bytes) B"
    }
    return ByteCountFormatter.string(
        fromByteCount: Int64(min(bytes, UInt64(Int64.max))),
        countStyle: .file
    )
}

func formatRate(_ bytesPerSecond: Double) -> String {
    let bounded = max(0, min(bytesPerSecond, Double(Int64.max)))
    if bounded < 100_000 {
        return String(format: "%.1f kB/s", bounded / 1_000)
    }
    if bounded < 1_000_000_000 {
        return String(format: "%.1f MB/s", bounded / 1_000_000)
    }
    return String(format: "%.1f GB/s", bounded / 1_000_000_000)
}
