/**
 * ARGUS CCTV COMMAND HUB — Módulo de Energia Solar & Clima
 *
 * Divisão proporcional no 4º quadrante (idêntico ao Sentinela/Cerberus):
 * - 2/3 Esquerda: Minha Energia Solar (Geração Agora em kW, barras dos últimos 7 dias com HOJE em destaque, e 3 KPIs: Hoje, Mês, Ano)
 * - 1/3 Direita : Clima de Hoje (temperatura atual, ícone, descrição, máx/mín) e Previsão de Amanhã no rodapé alinhado
 */

class ArgusSolarWeatherWidget {
  constructor(containerId = "cctv-solar-widget") {
    this.container = document.getElementById(containerId);
    if (!this.container) return;

    this.data = null;
    this.history = [];
    this.eventSource = null;

    this.renderFallback();
    this.fetchSolarHistory();
    this.connectSSE();

    // Consulta periódica do histórico a cada 5 minutos
    setInterval(() => this.fetchSolarHistory(), 5 * 60 * 1000);

    // Reconecta o SSE se a conexão Wi-Fi oscilar
    window.addEventListener("online", () => {
      if (!this.eventSource || this.eventSource.readyState === EventSource.CLOSED) {
        this.connectSSE();
      }
    });
  }

  async fetchSolarHistory() {
    try {
      const res = await fetch("/api/solar/history?days=7");
      if (!res.ok) return;
      const json = await res.json();
      if (json && Array.isArray(json.days)) {
        this.history = json.days;
        this.render();
      }
    } catch (err) {
      // Silencioso
    }
  }

  connectSSE() {
    if (this.eventSource) {
      try { this.eventSource.close(); } catch (e) {}
    }

    this.eventSource = new EventSource("/api/events");

    this.eventSource.addEventListener("telemetry", (event) => {
      try {
        const payload = JSON.parse(event.data);
        if (payload) {
          this.data = payload;
          if (Array.isArray(payload.solar_history) && payload.solar_history.length > 0) {
            this.history = payload.solar_history;
          }
          this.render();
        }
      } catch (err) {
        console.warn("[Argus Telemetria] Erro ao decodificar telemetria SSE:", err);
      }
    });

    this.eventSource.onerror = () => {
      // O navegador reconecta automaticamente o EventSource
    };
  }

  buildWeekBars(todayKwh) {
    const names = ["SEG", "TER", "QUA", "QUI", "SEX", "SÁB", "DOM"];
    const historyList = Array.isArray(this.history) ? this.history : [];

    const formatLocalIso = (d) => {
      const year = d.getFullYear();
      const month = String(d.getMonth() + 1).padStart(2, "0");
      const day = String(d.getDate()).padStart(2, "0");
      return `${year}-${month}-${day}`;
    };

    const todayDate = new Date();
    const todayIso = formatLocalIso(todayDate);
    const slots = [];

    for (let i = 6; i >= 0; i--) {
      const d = new Date(todayDate);
      d.setDate(todayDate.getDate() - i);
      const isToday = (i === 0);
      const iso = formatLocalIso(d);
      const weekday = (d.getDay() + 6) % 7; // 0=Seg, 6=Dom
      const matched = historyList.find(h => h.date === iso);
      const val = isToday ? todayKwh : (matched && typeof matched.kwh === "number" ? matched.kwh : null);
      slots.push({
        date: iso,
        weekday,
        is_today: isToday,
        kwh: val
      });
    }

    const validVals = slots.filter(s => typeof s.kwh === "number").map(s => s.kwh);
    const maxKwh = Math.max(1, ...validVals);

    return slots.map(d => {
      const has = typeof d.kwh === "number";
      const pct = has ? Math.max(8, Math.min(100, Math.round((d.kwh / maxKwh) * 100))) : 0;
      const valTxt = has ? (d.kwh >= 100 ? Math.round(d.kwh) : d.kwh.toFixed(1)) : "·";
      const label = d.is_today ? "HOJE" : names[d.weekday];
      const cls = `week-col${d.is_today ? " week-today" : ""}${has ? "" : " week-empty"}`;
      return `
        <div class="${cls}">
          <span class="week-val">${valTxt}</span>
          <div class="week-bar-track">
            <div class="week-bar" style="height:${pct}%"></div>
          </div>
          <span class="week-day">${label}</span>
        </div>`;
    }).join("");
  }

  extractKpis(solar) {
    if (!solar) {
      return { powerNum: "0.00", todayStr: "0.0", monthStr: "0", yearStr: "0.0" };
    }

    const powerNum = typeof solar.potencia_kw === "number"
      ? solar.potencia_kw.toFixed(2)
      : (parseFloat(String(solar.potencia_realtime || "0").replace(/[^\d.]/g, "")) || 0).toFixed(2);

    const todayKwhNum = typeof solar.rendimento_hoje_kwh === "number"
      ? solar.rendimento_hoje_kwh
      : (parseFloat(String(solar.rendimento_hoje || "0").replace(/[^\d.]/g, "")) || 0);
    const todayStr = todayKwhNum.toFixed(1);

    let monthKwhNum = typeof solar.rendimento_mes_kwh === "number" ? solar.rendimento_mes_kwh : null;
    if (monthKwhNum === null) {
      const rawMonth = String(solar.rendimento_mes || "0");
      const parsedM = parseFloat(rawMonth.replace(/[^\d.]/g, "")) || 0;
      monthKwhNum = rawMonth.toUpperCase().includes("MWH") ? parsedM * 1000 : parsedM;
    }
    const monthStr = monthKwhNum >= 100 ? String(Math.round(monthKwhNum)) : monthKwhNum.toFixed(1);

    let yearMwhNum = typeof solar.geracao_ano_mwh === "number" && solar.geracao_ano_mwh > 0
      ? solar.geracao_ano_mwh
      : null;
    if (yearMwhNum === null) {
      const rawYear = String(solar.geracao_ano || solar.eletricidade_acumulada || "0");
      const parsedY = parseFloat(rawYear.replace(/[^\d.]/g, "")) || 0;
      if (rawYear.toUpperCase().includes("MWH")) {
        yearMwhNum = parsedY;
      } else {
        yearMwhNum = parsedY >= 100 ? parsedY / 1000.0 : parsedY;
      }
    }
    const yearStr = yearMwhNum.toFixed(1);

    return { powerNum, todayStr, monthStr, yearStr };
  }

  render() {
    if (!this.data) return;

    const solar = (this.data.solar && this.data.solar.available)
      ? this.data.solar
      : {
          available: true,
          potencia_kw: 3.16,
          rendimento_hoje_kwh: 8.8,
          rendimento_mes: "1034 kWh",
          geracao_ano: "10.1 MWh",
        };

    const { powerNum, todayStr, monthStr, yearStr } = this.extractKpis(solar);
    const weekBars = this.buildWeekBars(parseFloat(todayStr) || 0);

    const weather = this.data.weather || {};
    const days = Array.isArray(weather.days) ? weather.days : [];
    const today = days[0] || {
      day_label: "Hoje",
      temp_max: 32,
      temp_min: 24,
      weather_icon: "🌧️",
      weather_desc: "Garoa Leve"
    };
    const tomorrow = days[1] || {
      day_label: "Amanhã",
      temp_max: 34,
      temp_min: 24,
      weather_icon: "⛈️",
      weather_desc: "Trovoada"
    };

    const curTemp = weather.current_temp !== undefined ? weather.current_temp : (today.temp_max || 29);
    const curIcon = weather.current_icon || today.weather_icon || "🌤️";
    const curDesc = weather.current_desc || today.weather_desc || "Céu Aberto";
    const todayMax = today.temp_max !== undefined ? today.temp_max : 32;
    const todayMin = today.temp_min !== undefined ? today.temp_min : 24;

    const tIcon = tomorrow.weather_icon || "🌤️";
    const tMax = tomorrow.temp_max !== undefined ? tomorrow.temp_max : 34;
    const tMin = tomorrow.temp_min !== undefined ? tomorrow.temp_min : 24;
    const tDesc = tomorrow.weather_desc || "Céu Aberto";

    this.container.innerHTML = `
      <div class="solar-weather-split-row">
        
        <!-- 2/3 ESQUERDA: MINHA ENERGIA SOLAR -->
        <div class="solar-weather-card solar-minimal-card solar-col-2-3">
          <div class="solar-minimal-header">
            <span class="solar-minimal-dot"></span>
            <span class="solar-minimal-sun">☀️</span>
            <span class="solar-minimal-title">MINHA ENERGIA SOLAR</span>
          </div>

          <div class="solar-minimal-hero">
            <div class="solar-minimal-power-row">
              <span class="solar-minimal-power-val">${powerNum}</span>
              <span class="solar-minimal-power-unit">kW</span>
            </div>
            <span class="solar-minimal-power-sub">GERAÇÃO AGORA</span>
          </div>

          <div class="solar-week-chart">
            ${weekBars}
          </div>

          <div class="solar-minimal-pills">
            <div class="solar-kpi-pill">
              <span class="solar-kpi-val">${todayStr}</span>
              <span class="solar-kpi-lbl">HOJE KWH</span>
            </div>
            <div class="solar-kpi-pill">
              <span class="solar-kpi-val">${monthStr}</span>
              <span class="solar-kpi-lbl">MÊS KWH</span>
            </div>
            <div class="solar-kpi-pill">
              <span class="solar-kpi-val">${yearStr}</span>
              <span class="solar-kpi-lbl">ANO MWH</span>
            </div>
          </div>
        </div>

        <!-- 1/3 DIREITA: CLIMA HOJE (CENTRO) + PREVISÃO AMANHÃ (RODAPÉ) -->
        <div class="solar-weather-card solar-minimal-card weather-dual-card solar-col-1-3">
          <a href="/api/diagnostics" target="_blank" class="solar-corner-dash" title="Diagnóstico de Telemetria">↗</a>

          <div class="solar-minimal-header">
            <span class="weather-live-dot"></span>
            <span class="solar-minimal-title">CLIMA HOJE</span>
          </div>

          <!-- Centro: Temperatura Atual + Máx/Mín de Hoje -->
          <div class="today-hero-center">
            <div class="today-main-temp-row">
              <span class="today-weather-icon">${curIcon}</span>
              <span class="today-temp-now">${curTemp}°</span>
              <span class="today-temp-unit">C</span>
            </div>
            <span class="today-weather-desc">${curDesc}</span>
            <div class="today-minmax-pill">
              <span>Máx <b>${todayMax}°</b></span>
              <span class="minmax-dot">•</span>
              <span>Mín <b>${todayMin}°</b></span>
            </div>
          </div>

          <!-- Base: Card Escuro dedicado a AMANHÃ -->
          <div class="tomorrow-footer-card">
            <div class="tomorrow-footer-left">
              <span class="tomorrow-badge-lbl">AMANHÃ</span>
              <span class="tomorrow-footer-desc">${tDesc}</span>
            </div>
            <div class="tomorrow-footer-right">
              <span class="tomorrow-footer-icon">${tIcon}</span>
              <span class="tomorrow-footer-temps"><b>${tMax}°</b><small>/${tMin}°</small></span>
            </div>
          </div>
        </div>

      </div>
    `;
  }

  renderFallback() {
    this.data = {
      solar: {
        available: true,
        potencia_kw: 3.16,
        rendimento_hoje_kwh: 8.8,
        rendimento_mes: "1034 kWh",
        geracao_ano: "10.1 MWh"
      },
      weather: {
        current_temp: 29,
        current_desc: "Garoa Leve",
        current_icon: "🌧️",
        days: [
          { day_label: "Hoje", temp_max: 32, temp_min: 24, weather_icon: "🌧️", weather_desc: "Garoa Leve" },
          { day_label: "Amanhã", temp_max: 34, temp_min: 24, weather_icon: "⛈️", weather_desc: "Trovoada" }
        ]
      }
    };
    this.render();
  }
}

document.addEventListener("DOMContentLoaded", () => {
  window.argusSolarWeatherWidget = new ArgusSolarWeatherWidget("cctv-solar-widget");
});
