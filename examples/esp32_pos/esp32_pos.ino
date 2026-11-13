/*
 * ==============================================================================
 * M-Tap Edge POS Terminal Client for ESP32 (Arduino Framework)
 * Author: Boron Labs Systems Engineering
 * 
 * Hardware Compatibility: ESP32 / ESP32-S3 / ESP32-C3
 * Dependencies:
 *   - Built-in WiFi.h
 *   - Built-in HTTPClient.h
 *   - Built-in mbedtls (for hardware-accelerated HMAC-SHA256)
 * ==============================================================================
 */

#include <WiFi.h>
#include <HTTPClient.h>
#include <time.h>
#include "mbedtls/md.h"

// -----------------------------------------------------------------------------
// 1. Configuration & Credentials
// -----------------------------------------------------------------------------
const char* WIFI_SSID     = "YOUR_WIFI_SSID";
const char* WIFI_PASSWORD = "YOUR_WIFI_PASSWORD";

// Server endpoint (Render live URL or local development IP, e.g., http://192.168.1.100:8080)
const char* SERVER_URL    = "https://m-tap-backend.onrender.com/api/v1/terminal/tap";

// Terminal credentials (must match 'terminals' table in PostgreSQL)
const char* TERMINAL_ID   = "POD_001";
const char* HMAC_PSK_KEY  = "secret_hmac_psk_key_boron_labs_2026";

// NTP time configuration (Required for the 30s anti-replay window)
const char* NTP_SERVER    = "pool.ntp.org";
const long  GMT_OFFSET_SEC = 0; // UTC for Ghana (GMT)
const int   DAYLIGHT_OFFSET = 0;

// -----------------------------------------------------------------------------
// 2. Helper: Compute HMAC-SHA256 using ESP32's native mbedtls
// -----------------------------------------------------------------------------
String computeHMACSHA256(const String &data, const String &key) {
  byte hmacResult[32];

  mbedtls_md_context_t ctx;
  mbedtls_md_type_t md_type = MBEDTLS_MD_SHA256;

  mbedtls_md_init(&ctx);
  mbedtls_md_setup(&ctx, mbedtls_md_info_from_type(md_type), 1);
  mbedtls_md_hmac_starts(&ctx, (const unsigned char*) key.c_str(), key.length());
  mbedtls_md_hmac_update(&ctx, (const unsigned char*) data.c_str(), data.length());
  mbedtls_md_hmac_finish(&ctx, hmacResult);
  mbedtls_md_free(&ctx);

  // Convert binary digest to lowercase hexadecimal string
  String hexStr = "";
  for (int i = 0; i < 32; i++) {
    char buf[3];
    sprintf(buf, "%02x", hmacResult[i]);
    hexStr += buf;
  }
  return hexStr;
}

// -----------------------------------------------------------------------------
// 3. Helper: Generate Pseudo-UUIDv4 for X-Idempotency-Key
// -----------------------------------------------------------------------------
String generateUUIDv4() {
  uint32_t r1 = esp_random();
  uint32_t r2 = esp_random();
  uint32_t r3 = esp_random();
  uint32_t r4 = esp_random();

  char uuid[37];
  snprintf(uuid, sizeof(uuid), "%08x-%04x-4%03x-%04x-%04x%08x",
           r1,
           (r2 >> 16) & 0xFFFF,
           r2 & 0x0FFF,
           ((r3 >> 16) & 0x3FFF) | 0x8000,
           r3 & 0xFFFF,
           r4);
  return String(uuid);
}

// -----------------------------------------------------------------------------
// 4. Core Function: Transmit NFC Tap to M-Tap Switching Engine
// -----------------------------------------------------------------------------
bool sendTap(const char* tagUID, int64_t amountPesewas) {
  // 1. Obtain current UTC epoch timestamp
  time_t now;
  time(&now);
  if (now < 100000) {
    Serial.println("[ERROR] NTP clock not synchronized yet");
    return false;
  }
  String timestampStr = String(now);

  // 2. Build JSON Request Body
  // Example: {"terminal_id":"POD_001","tag_uid":"04A38CF2196880","amount_pesewas":2500}
  String body = "{\"terminal_id\":\"" + String(TERMINAL_ID) +
                "\",\"tag_uid\":\"" + String(tagUID) +
                "\",\"amount_pesewas\":" + String((unsigned long)amountPesewas) + "}";

  // 3. Compute Anti-Replay Signature: HMAC-SHA256(PSK, timestamp + "." + body)
  String stringToSign = timestampStr + "." + body;
  String signature = computeHMACSHA256(stringToSign, HMAC_PSK_KEY);

  // 4. Generate unique idempotency key
  String idempotencyKey = generateUUIDv4();

  Serial.println("\n--- [DISPATCHING TAP TO SWITCH] ---");
  Serial.println("Timestamp:        " + timestampStr);
  Serial.println("Payload:          " + body);
  Serial.println("Signature:        " + signature);
  Serial.println("Idempotency-Key:  " + idempotencyKey);

  // 5. Send HTTP POST Request
  HTTPClient http;
  http.begin(SERVER_URL);
  http.addHeader("Content-Type", "application/json");
  http.addHeader("X-Terminal-Id", TERMINAL_ID);
  http.addHeader("X-Timestamp", timestampStr);
  http.addHeader("X-Signature", signature);
  http.addHeader("X-Idempotency-Key", idempotencyKey);

  int httpCode = http.POST(body);

  if (httpCode > 0) {
    String response = http.getString();
    Serial.printf("[HTTP %d] Response: %s\n", httpCode, response.c_str());

    if (httpCode == 202) {
      Serial.println(">>> [SUCCESS] Tap authorized! MoMo charge initiated. <<<");
      http.end();
      return true;
    } else if (httpCode == 429) {
      Serial.println(">>> [THROTTLED] Terminal rate limit (1 tap per 3s). <<<");
    } else if (httpCode == 409) {
      Serial.println(">>> [CONFLICT] Tap is already in flight. <<<");
    }
  } else {
    Serial.printf("[ERROR] HTTP POST failed: %s\n", http.errorToString(httpCode).c_str());
  }

  http.end();
  return false;
}

// -----------------------------------------------------------------------------
// 5. Setup & Loop
// -----------------------------------------------------------------------------
void setup() {
  Serial.begin(115200);
  delay(1000);
  Serial.println("\n==============================================");
  Serial.println("   M-Tap ESP32 Edge POS Terminal Initializing ");
  Serial.println("==============================================");

  // Connect to Wi-Fi
  Serial.printf("Connecting to Wi-Fi: %s ", WIFI_SSID);
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
  }
  Serial.println("\nWi-Fi Connected! IP: " + WiFi.localIP().toString());

  // Synchronize system clock with NTP
  Serial.println("Synchronizing clock via NTP...");
  configTime(GMT_OFFSET_SEC, DAYLIGHT_OFFSET, NTP_SERVER);

  struct tm timeinfo;
  while (!getLocalTime(&timeinfo)) {
    Serial.print(".");
    delay(500);
  }
  Serial.println("\nTime Synchronized successfully!");
  Serial.printf("Current UTC Time: %02d:%02d:%02d\n", timeinfo.tm_hour, timeinfo.tm_min, timeinfo.tm_sec);

  // Trigger an initial simulated tap test
  delay(2000);
  Serial.println("\nExecuting Initial Bench Test Tap...");
  sendTap("04A38CF2196880", 2500); // 2500 Pesewas = GHS 25.00
}

void loop() {
  // In a production setup, poll your NFC card reader here (e.g. PN532 or RC522).
  // For bench testing, tap automatically every 10 seconds:
  delay(10000);
  Serial.println("\n[Triggering Scheduled Periodic Tap]");
  sendTap("04A38CF2196880", 1500); // 1500 Pesewas = GHS 15.00
}
