/**
 * NimOS Torrent Daemon (nimos-torrentd)
 * 
 * Native BitTorrent daemon using libtorrent-rasterbar.
 * Communicates via Unix socket — no open ports.
 * NimOS API server proxies requests from the UI.
 * 
 * Socket: /run/nimos/torrent.sock
 * 
 * API:
 *   GET  /torrents          — list all torrents
 *   POST /torrent/add       — add magnet or .torrent
 *   POST /torrent/pause     — pause by hash
 *   POST /torrent/resume    — resume by hash
 *   POST /torrent/remove    — remove by hash (optional delete_files)
 *   GET  /stats             — global download/upload rates
 *   POST /settings          — update limits
 */

#include "torrent_engine.h"
#include "httplib.h"
#include <iostream>
#include <sstream>
#include <csignal>
#include <filesystem>
#include <fstream>

namespace fs = std::filesystem;

// ═══════════════════════════════════
// Globals
// ═══════════════════════════════════

static const char* LISTEN_HOST = "127.0.0.1";
static const int   LISTEN_PORT = 9091;
static const char* CONFIG_PATH = "/etc/nimos/torrent.conf";
static const char* STATE_PATH  = "/var/lib/nimos/torrentd/state";
static const char* PID_FILE    = "/run/nimos/torrentd.pid";

static TorrentEngine* g_engine = nullptr;
static httplib::Server* g_server = nullptr;

// ═══════════════════════════════════
// JSON helpers
// ═══════════════════════════════════

static std::string escape_json(const std::string& s) {
    std::string out;
    for (char c : s) {
        switch (c) {
            case '"':  out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\n': out += "\\n"; break;
            default:   out += c;
        }
    }
    return out;
}

static std::string torrent_to_json(const TorrentInfo& t) {
    std::stringstream j;
    j << "{";
    j << "\"hash\":\"" << t.hash << "\",";
    j << "\"name\":\"" << escape_json(t.name) << "\",";
    j << "\"state\":\"" << t.state << "\",";
    j << "\"progress\":" << t.progress << ",";
    j << "\"download_rate\":" << t.download_rate << ",";
    j << "\"upload_rate\":" << t.upload_rate << ",";
    j << "\"total_done\":" << t.total_done << ",";
    j << "\"total_wanted\":" << t.total_wanted << ",";
    j << "\"peers\":" << t.num_peers << ",";
    j << "\"seeds\":" << t.num_seeds << ",";
    j << "\"save_path\":\"" << escape_json(t.save_path) << "\",";
    j << "\"paused\":" << (t.paused ? "true" : "false");
    j << "}";
    return j.str();
}

// ═══════════════════════════════════
// Signal handling — graceful shutdown
// ═══════════════════════════════════

static void signal_handler(int sig) {
    std::cout << "\n[torrentd] Shutting down (signal " << sig << ")...\n";
    if (g_engine) {
        g_engine->saveState();
    }
    if (g_server) {
        g_server->stop();
    }
    unlink(PID_FILE);
    exit(0);
}

// ═══════════════════════════════════
// Parse simple JSON body (key:value)
// ═══════════════════════════════════

static std::string get_json_string(const std::string& body, const std::string& key) {
    std::string search = "\"" + key + "\"";
    auto pos = body.find(search);
    if (pos == std::string::npos) return "";

    pos = body.find(':', pos);
    if (pos == std::string::npos) return "";

    // Find value start
    pos = body.find_first_not_of(" \t\n\r", pos + 1);
    if (pos == std::string::npos) return "";

    if (body[pos] == '"') {
        // String value
        auto end = body.find('"', pos + 1);
        if (end == std::string::npos) return "";
        return body.substr(pos + 1, end - pos - 1);
    }

    // Non-string value
    auto end = body.find_first_of(",}\n", pos);
    return body.substr(pos, end - pos);
}

static bool get_json_bool(const std::string& body, const std::string& key) {
    return get_json_string(body, key) == "true";
}

static int get_json_int(const std::string& body, const std::string& key) {
    auto val = get_json_string(body, key);
    try { return std::stoi(val); } catch (...) { return 0; }
}

// ═══════════════════════════════════
// Main
// ═══════════════════════════════════

int main() {
    // Ensure runtime directories exist
    fs::create_directories("/run/nimos");
    fs::create_directories(STATE_PATH);

    // Write PID file
    {
        std::ofstream pid(PID_FILE);
        pid << getpid();
    }

    // Signal handlers
    signal(SIGINT, signal_handler);
    signal(SIGTERM, signal_handler);

    // Create engine
    TorrentEngine engine(CONFIG_PATH, STATE_PATH);
    g_engine = &engine;

    // Create HTTP server
    httplib::Server svr;
    g_server = &svr;

    // ─── Routes ───

    // List all torrents
    svr.Get("/torrents", [&](const httplib::Request&, httplib::Response& res) {
        auto torrents = engine.list();
        std::stringstream j;
        j << "[";
        bool first = true;
        for (auto& t : torrents) {
            if (!first) j << ",";
            first = false;
            j << torrent_to_json(t);
        }
        j << "]";
        res.set_content(j.str(), "application/json");
    });

    // Global stats
    svr.Get("/stats", [&](const httplib::Request&, httplib::Response& res) {
        auto torrents = engine.list();
        int64_t total_dl = 0, total_ul = 0;
        int active = 0, seeding = 0, paused = 0;
        for (auto& t : torrents) {
            total_dl += t.download_rate;
            total_ul += t.upload_rate;
            if (t.paused) paused++;
            else if (t.state == "seeding") seeding++;
            else active++;
        }
        std::stringstream j;
        j << "{";
        j << "\"total\":" << torrents.size() << ",";
        j << "\"active\":" << active << ",";
        j << "\"seeding\":" << seeding << ",";
        j << "\"paused\":" << paused << ",";
        j << "\"download_rate\":" << total_dl << ",";
        j << "\"upload_rate\":" << total_ul;
        j << "}";
        res.set_content(j.str(), "application/json");
    });

    // Add torrent (magnet or .torrent file path)
    svr.Post("/torrent/add", [&](const httplib::Request& req, httplib::Response& res) {
        auto magnet = get_json_string(req.body, "magnet");
        auto file = get_json_string(req.body, "file");
        auto save_path = get_json_string(req.body, "save_path");

        std::string hash;
        if (!magnet.empty()) {
            hash = engine.addMagnet(magnet, save_path);
        } else if (!file.empty()) {
            hash = engine.addTorrentFile(file, save_path);
        } else {
            res.set_content("{\"error\":\"Provide magnet or file\"}", "application/json");
            return;
        }
        res.set_content("{\"ok\":true,\"hash\":\"" + hash + "\"}", "application/json");
    });

    // Pause
    svr.Post("/torrent/pause", [&](const httplib::Request& req, httplib::Response& res) {
        auto hash = get_json_string(req.body, "hash");
        bool ok = engine.pause(hash);
        res.set_content(ok ? "{\"ok\":true}" : "{\"error\":\"Not found\"}", "application/json");
    });

    // Resume
    svr.Post("/torrent/resume", [&](const httplib::Request& req, httplib::Response& res) {
        auto hash = get_json_string(req.body, "hash");
        bool ok = engine.resume(hash);
        res.set_content(ok ? "{\"ok\":true}" : "{\"error\":\"Not found\"}", "application/json");
    });

    // Remove
    svr.Post("/torrent/remove", [&](const httplib::Request& req, httplib::Response& res) {
        auto hash = get_json_string(req.body, "hash");
        bool delete_files = get_json_bool(req.body, "delete_files");
        bool ok = engine.remove(hash, delete_files);
        res.set_content(ok ? "{\"ok\":true}" : "{\"error\":\"Not found\"}", "application/json");
    });

    // Update settings
    svr.Post("/settings", [&](const httplib::Request& req, httplib::Response& res) {
        int dl = get_json_int(req.body, "download_limit");
        int ul = get_json_int(req.body, "upload_limit");
        int max = get_json_int(req.body, "max_active");
        if (dl >= 0) engine.setDownloadLimit(dl);
        if (ul >= 0) engine.setUploadLimit(ul);
        if (max > 0) engine.setMaxActive(max);
        res.set_content("{\"ok\":true}", "application/json");
    });

    // Save state (called periodically or before shutdown)
    svr.Post("/save", [&](const httplib::Request&, httplib::Response& res) {
        engine.saveState();
        res.set_content("{\"ok\":true}", "application/json");
    });

    // ─── Start listening on localhost only ───
    std::cout << "[torrentd] NimOS Torrent Daemon starting...\n";
    std::cout << "[torrentd] Listening: " << LISTEN_HOST << ":" << LISTEN_PORT << "\n";
    std::cout << "[torrentd] State:  " << STATE_PATH << "\n";
    std::cout << "[torrentd] Ready.\n";

    svr.listen(LISTEN_HOST, LISTEN_PORT);

    // Cleanup
    unlink(PID_FILE);
    return 0;
}
