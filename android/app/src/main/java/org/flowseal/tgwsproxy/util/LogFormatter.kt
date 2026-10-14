package org.flowseal.tgwsproxy.util

import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

object LogFormatter {

    private val goLogPrefixRegex = Regex("""^\d{4}/\d{2}/\d{2}\s+(\d{2}:\d{2}:\d{2})\s+(.*)$""")
    private val localSocketRegex = Regex("""\[127\.0\.0\.1:\d+\]\s*""")
    private val timeFormat = SimpleDateFormat("HH:mm:ss", Locale.getDefault())

    fun format(rawLine: String): String? {
        val trimmed = rawLine.trim()
        if (trimmed.isEmpty()) return null

        // 1. Filter out raw stats spam (metrics are displayed on dashboard cards)
        if (trimmed.startsWith("[Stats]") || trimmed.contains("Active:") && trimmed.contains("Total:")) {
            return null
        }

        // 2. Filter out ASCII banners and decorative dividers
        if (trimmed.startsWith("===") || trimmed.startsWith("TG WS Proxy (Go Mobile Core)")) {
            return null
        }

        // 3. Extract timestamp
        var timeStr = ""
        var content = trimmed

        val match = goLogPrefixRegex.find(trimmed)
        if (match != null) {
            timeStr = match.groupValues[1]
            content = match.groupValues[2].trim()
        } else {
            timeStr = timeFormat.format(Date())
        }

        // Clean local ephemeral port prefixes like [127.0.0.1:54321]
        content = localSocketRegex.replace(content, "")

        // 4. Translate and enrich into high-value messages
        val formatted = when {
            // Already formatted with emojis
            content.startsWith("🚀") || content.startsWith("✅") || content.startsWith("⚠️") ||
            content.startsWith("❌") || content.startsWith("🛑") || content.startsWith("📶") ||
            content.startsWith("📊") || content.startsWith("🔄") || content.startsWith("🛡️") ||
            content.startsWith("🟢") || content.startsWith("📷") -> {
                content
            }

            // Core startup & readiness
            content.contains("Ready on") || content.startsWith("Запуск ядра:") -> {
                val addrMatch = Regex("""(?:Ready on|Запуск ядра:)\s*([^\s]+)""").find(content)
                val sniMatch = Regex("""SNI:\s*([^\s)]+)""").find(content)
                val addr = addrMatch?.groupValues?.get(1) ?: "127.0.0.1:1443"
                val sni = sniMatch?.groupValues?.get(1) ?: "google.com"
                "🚀 Прокси запущен на $addr (маскировка: $sni)"
            }

            content.contains("Listening on") -> {
                val addrMatch = Regex("""Listening on\s*([^\s]+)""").find(content)
                val addr = addrMatch?.groupValues?.get(1) ?: "127.0.0.1:1443"
                "🚀 Слушает локальный порт $addr"
            }

            content.contains("Telegram link:") -> {
                val link = content.substringAfter("Telegram link:").trim()
                "🔗 Ссылка прокси: $link"
            }

            content.contains("CF gateway mirrors") || content.contains("Dynamic CF gateway domains") -> {
                val countMatch = Regex("""(\d+)""").find(content)
                val count = countMatch?.groupValues?.get(1) ?: "20"
                "🛡️ Загружено $count резервных зеркал Cloudflare"
            }

            // Successful Telegram connections
            content.contains("Connected DC") && content.contains("(chat)") -> {
                val dcMatch = Regex("""Connected DC(\d+)""").find(content)
                val domainMatch = Regex("""via\s+([^\s]+)""").find(content)
                val dc = dcMatch?.groupValues?.get(1) ?: "2"
                val domain = domainMatch?.groupValues?.get(1) ?: "шлюз"
                "🟢 DC$dc (сообщения) подключен через $domain"
            }

            content.contains("Connected DC") && content.contains("(media)") -> {
                val dcMatch = Regex("""Connected DC(\d+)""").find(content)
                val domainMatch = Regex("""via\s+([^\s]+)""").find(content)
                val dc = dcMatch?.groupValues?.get(1) ?: "4"
                val domain = domainMatch?.groupValues?.get(1) ?: "шлюз"
                "📷 DC$dc (медиа/файлы) подключен через $domain"
            }

            content.contains("Connected via CF Worker") -> {
                val workerMatch = Regex("""CF Worker\s+([^\s]+)\s+for DC(\d+)""").find(content)
                val worker = workerMatch?.groupValues?.get(1) ?: ""
                val dc = workerMatch?.groupValues?.get(2) ?: ""
                "🌐 DC$dc подключен через Cloudflare Worker $worker"
            }

            // Session closed
            content.contains("Session closed:") -> {
                val tagMatch = Regex("""\[([^]]+)]""").find(content)
                val statsMatch = Regex("""Session closed:\s*\^([^,]+),\s*v([^\s]+)\s+in\s+([\d.]+)s""").find(content)
                val tag = tagMatch?.groupValues?.get(1) ?: "DC"
                if (statsMatch != null) {
                    val up = statsMatch.groupValues[1]
                    val down = statsMatch.groupValues[2]
                    val sec = statsMatch.groupValues[3]
                    "📶 $tag: сессия завершена (↑ $up, ↓ $down за ${sec}с)"
                } else {
                    "📶 $tag: сессия завершена"
                }
            }

            // Warnings & Errors
            content.contains("Fake TLS verify failed") || content.contains("secret mismatch") -> {
                "⚠️ Несовпадение ключа Fake TLS (клиент Telegram использует старый ключ)"
            }

            content.contains("Bad handshake") -> {
                "⚠️ Ошибка MTProto рукопожатия от клиента"
            }

            content.contains("unreachable") || content.contains("No upstream gateways reachable") -> {
                "❌ Серверы Telegram недоступны (проверьте интернет или обновите зеркала)"
            }

            content.contains("Server shutdown cleanly") || content.startsWith("Остановка ядра") -> {
                "🛑 Служба остановлена"
            }

            content.contains("Ошибка запуска") -> {
                "❌ $content"
            }

            else -> {
                // Remove [Proxy] tag if present
                val cleaned = content.replace("[Proxy]", "").trim()
                if (cleaned.isEmpty()) return null
                "ℹ️ $cleaned"
            }
        }

        return "[$timeStr] $formatted"
    }
}
