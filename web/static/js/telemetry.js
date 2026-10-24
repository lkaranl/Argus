/**
 * ARGUS CCTV COMMAND HUB — Consumidor de Telemetria SSE em Tempo Real
 * Atualização cirúrgica de nós de texto folha via textContent para zero reflow na GPU.
 */

class ArgusTelemetryConsumer {
  constructor(containerId = "cctv-solar-widget") {
    this.container = document.getElementById(containerId);
    this.uptimeElement = document.getElementById("server-uptime");
    this.initialized = false;
    this.eventSource = null;

    this.renderSkeleton();
    this.connect();
  }

  renderSkeleton() {
    if (!this.container) return;
    this.container.innerHTML = `
      <div class="tesla-solar-card">
        <div class="solar-card-header">
          <div class="solar-title-group">
            <span class="solar-icon-sun">☀️</span>
            <div class="solar-titles">
              <span class="solar-card-title" id="solar-station-name">USINA SOLAR</span>
              <span class="solar-card-subtitle" id="solar-location-subtitle">Conectando telemetria...</span>
            </div>
          </div>
          <span class="tesla-live-beacon" id="solar-live-badge">
            <span class="beacon-dot"></span> <span id="beacon-text">AO VIVO</span>
          </span>
        </div>

        <div class="solar-hero-metrics">
          <div class="solar-hero-stat">
            <span class="hero-stat-label">GERAÇÃO AGORA</span>
            <div class="hero-stat-val-wrap">
              <span class="hero-stat-icon">⚡</span>
              <span class="hero-stat-number stat-power" id="solar-stat-power">0.00</span>
              <span class="hero-stat-unit">kW</span>
            </div>
          </div>

          <div class="solar-hero-divider"></div>

          <div class="solar-hero-stat">
            <span class="hero-stat-label">PRODUZIDO HOJE</span>
            <div class="hero-stat-val-wrap">
              <span class="hero-stat-icon">☀️</span>
              <span class="hero-stat-number stat-yield" id="solar-stat-yield">0.0</span>
              <span class="hero-stat-unit">kWh</span>
            </div>
          </div>
        </div>

        <div class="solar-forecast-strip" id="solar-forecast-strip">
          <div class="forecast-strip-item">
            <span class="forecast-day-name" id="forecast-d1-label">Amanhã</span>
            <span class="forecast-day-icon" id="forecast-d1-icon">🌤️</span>
            <span class="forecast-day-temp" id="forecast-d1-temp">--°<small>/--°</small></span>
            <span class="forecast-pill" id="forecast-d1-pill" style="color: #FFB300; border-color: rgba(255, 179, 0, 0.4)">ALTO</span>
          </div>

          <div class="forecast-strip-sep"></div>

          <div class="forecast-strip-item">
            <span class="forecast-day-name" id="forecast-d2-label">Depois</span>
            <span class="forecast-day-icon" id="forecast-d2-icon">⛅</span>
            <span class="forecast-day-temp" id="forecast-d2-temp">--°<small>/--°</small></span>
            <span class="forecast-pill" id="forecast-d2-pill" style="color: #FFA000; border-color: rgba(255, 160, 0, 0.4)">BOM</span>
          </div>
        </div>
      </div>
    `;

    // Cache de referências aos elementos folha
    this.elStationName = document.getElementById("solar-station-name");
    this.elSubtitle = document.getElementById("solar-location-subtitle");
    this.elPower = document.getElementById("solar-stat-power");
    this.elYield = document.getElementById("solar-stat-yield");

    this.elD1Label = document.getElementById("forecast-d1-label");
    this.elD1Icon = document.getElementById("forecast-d1-icon");
    this.elD1Temp = document.getElementById("forecast-d1-temp");
    this.elD1Pill = document.getElementById("forecast-d1-pill");

    this.elD2Label = document.getElementById("forecast-d2-label");
    this.elD2Icon = document.getElementById("forecast-d2-icon");
    this.elD2Temp = document.getElementById("forecast-d2-temp");
    this.elD2Pill = document.getElementById("forecast-d2-pill");

    this.elBeaconText = document.getElementById("beacon-text");
  }

  connect() {
    if (this.eventSource) {
      try { this.eventSource.close(); } catch (e) {}
    }

    this.eventSource = new EventSource("/api/events");

    this.eventSource.addEventListener("telemetry", (event) => {
      try {
        const data = JSON.parse(event.data);
        this.updateDOM(data);
      } catch (err) {
        console.warn("[Argus Telemetria] Erro ao decodificar JSON SSE:", err);
      }
    });

    this.eventSource.onerror = () => {
      if (this.elBeaconText) {
        this.elBeaconText.textContent = "RECONECTANDO";
      }
    };

    this.eventSource.onopen = () => {
      if (this.elBeaconText) {
        this.elBeaconText.textContent = "AO VIVO";
      }
    };
  }

  updateDOM(data) {
    if (!data) return;

    // 1. Atualização do Uptime do Servidor
    if (data.server && this.uptimeElement) {
      this.uptimeElement.textContent = `UPTIME: ${data.server.uptime_human || "--"}`;
    }

    // 2. Atualização dos Dados Fotovoltaicos
    const solar = data.solar;
    if (solar && solar.available) {
      const station = (solar.station_name || "USINA SOLAR").toUpperCase();
      if (this.elStationName) this.elStationName.textContent = station;

      const powerNum = typeof solar.potencia_kw === "number"
        ? solar.potencia_kw.toFixed(2)
        : (String(solar.potencia_realtime || "0.00").replace(/[^\d.]/g, "") || "0.00");
      if (this.elPower) this.elPower.textContent = powerNum;

      const yieldNum = typeof solar.rendimento_hoje_kwh === "number"
        ? solar.rendimento_hoje_kwh.toFixed(1)
        : (String(solar.rendimento_hoje || "0.0").replace(/[^\d.]/g, "") || "0.0");
      if (this.elYield) this.elYield.textContent = yieldNum;
    }

    // 3. Atualização Climática e Subtítulo
    const weather = data.weather;
    if (weather) {
      const city = weather.city || (solar && solar.endereco) || "Brasil";
      const temp = weather.current_temp !== undefined ? `${weather.current_temp}°C` : "";
      const icon = weather.current_icon || "🌤️";
      const desc = weather.current_desc || "";

      if (this.elSubtitle) {
        this.elSubtitle.textContent = `${city} • ${icon} ${temp} ${desc}`.trim();
      }

      // Previsão dos próximos dias (Amanhã e Depois)
      if (weather.days && weather.days.length >= 3) {
        const d1 = weather.days[1];
        const d2 = weather.days[2];

        if (d1 && this.elD1Label) {
          this.elD1Label.textContent = d1.day_label || "Amanhã";
          this.elD1Icon.textContent = d1.weather_icon || "🌤️";
          this.elD1Temp.innerHTML = `${d1.temp_max}°<small>/${d1.temp_min}°</small>`;
          this.elD1Pill.textContent = (d1.solar_potential || "BOM").toUpperCase();
          this.elD1Pill.style.color = d1.solar_color || "#FFB300";
          this.elD1Pill.style.borderColor = (d1.solar_color || "#FFB300") + "60";
        }

        if (d2 && this.elD2Label) {
          this.elD2Label.textContent = d2.day_label || "Depois";
          this.elD2Icon.textContent = d2.weather_icon || "⛅";
          this.elD2Temp.innerHTML = `${d2.temp_max}°<small>/${d2.temp_min}°</small>`;
          this.elD2Pill.textContent = (d2.solar_potential || "BOM").toUpperCase();
          this.elD2Pill.style.color = d2.solar_color || "#FFA000";
          this.elD2Pill.style.borderColor = (d2.solar_color || "#FFA000") + "60";
        }
      }
    }
  }
}

document.addEventListener("DOMContentLoaded", () => {
  window.argusTelemetry = new ArgusTelemetryConsumer("cctv-solar-widget");
});
