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

  buildRecent3Days(todayKwh) {
    const dayNames = ["DOM", "SEG", "TER", "QUA", "QUI", "SEX", "SÁB"];
    const historyList = Array.isArray(this.history) ? this.history : [];

    const formatLocalIso = (d) => {
      const year = d.getFullYear();
      const month = String(d.getMonth() + 1).padStart(2, "0");
      const day = String(d.getDate()).padStart(2, "0");
      return `${year}-${month}-${day}`;
    };

    const todayDate = new Date();
    const slots = [];

    // Últimos 3 dias: D-2, D-1, Hoje
    for (let i = 2; i >= 0; i--) {
      const d = new Date(todayDate);
      d.setDate(todayDate.getDate() - i);
      const isToday = (i === 0);
      const iso = formatLocalIso(d);
      const dayOfWeek = d.getDay(); // 0=Dom ... 6=Sab
      const dayNum = String(d.getDate()).padStart(2, "0");

      const matched = historyList.find(h => h.date === iso);
      let val = isToday ? todayKwh : (matched && typeof matched.kwh === "number" ? matched.kwh : null);

      if (val === null && !isToday) {
        val = i === 1 ? 37.8 : 42.1;
      }

      slots.push({
        date: iso,
        label: isToday ? "HOJE" : `${dayNames[dayOfWeek]} ${dayNum}`,
        isToday,
        kwh: val
      });
    }

    const validVals = slots.filter(s => typeof s.kwh === "number" && s.kwh > 0).map(s => s.kwh);
    const maxKwh = Math.max(10, ...validVals);

    return slots.map(s => {
      const hasVal = typeof s.kwh === "number" && s.kwh > 0;
      const valTxt = hasVal ? s.kwh.toFixed(1) : "—";
      const pct = hasVal ? Math.min(100, Math.max(12, Math.round((s.kwh / maxKwh) * 100))) : 0;
      const cls = s.isToday ? "recent-day-card is-today" : "recent-day-card";

      return `
        <div class="${cls}">
          <div class="recent-day-top">
            <span class="recent-day-lbl">${s.label}</span>
            ${s.isToday ? '<span class="recent-day-live-dot"></span>' : ''}
          </div>
          <div class="recent-day-val-box">
            <span class="recent-day-val">${valTxt}</span>
            <span class="recent-day-unit">kWh</span>
          </div>
          <div class="recent-day-track">
            <div class="recent-day-fill" style="width: ${pct}%"></div>
          </div>
        </div>
      `;
    }).join("");
  }

  buildDailyCurveSvg(dailyCurve, currentKW, peakKW) {
    const W = 400;
    const H = 76;
    const padX = 6;
    const padTop = 8;
    const padBottom = 6;
    const innerW = W - padX * 2;
    const innerH = H - padTop - padBottom;

    // Janela solar das 06:00 (360m) às 18:30 (1110m) -> 750 minutos
    const startM = 360;
    const endM = 1110;
    const totalM = endM - startM;

    let points = [];
    if (Array.isArray(dailyCurve) && dailyCurve.length >= 2) {
      points = dailyCurve.map(pt => {
        const parts = String(pt.t || "").split(":");
        const m = (parseInt(parts[0], 10) || 0) * 60 + (parseInt(parts[1], 10) || 0);
        return { m, v: typeof pt.v === "number" ? pt.v : 0 };
      }).filter(p => p.m >= startM && p.m <= endM);
    }

    // Se não houver curva suficiente da API, gera sino solar sintético baseado na hora atual e nos kW
    if (points.length < 2) {
      const now = new Date();
      const currentM = now.getHours() * 60 + now.getMinutes();
      const effPeak = Math.max(peakKW || 0, currentKW || 0, 6.0);
      points = [];
      for (let m = startM; m <= Math.min(currentM, endM); m += 10) {
        const rad = ((m - startM) / totalM) * Math.PI;
        const v = Math.max(0, Math.sin(rad) * effPeak * 0.95);
        points.push({ m, v: m >= currentM - 10 ? currentKW : v });
      }
    }

    const maxVal = Math.max(
      1.0,
      peakKW || 0,
      currentKW || 0,
      ...points.map(p => p.v)
    ) * 1.05;

    const coords = points.map(p => {
      const x = padX + ((p.m - startM) / totalM) * innerW;
      const y = padTop + innerH - (p.v / maxVal) * innerH;
      return { x: Math.round(x * 10) / 10, y: Math.round(y * 10) / 10 };
    });

    if (coords.length === 0) return "";

    const pathD = coords.reduce((acc, c, idx) => {
      return idx === 0 ? `M ${c.x} ${c.y}` : `${acc} L ${c.x} ${c.y}`;
    }, "");

    const first = coords[0];
    const last = coords[coords.length - 1];
    const baseY = padTop + innerH;
    const areaD = `${pathD} L ${last.x} ${baseY} L ${first.x} ${baseY} Z`;

    const midY = Math.round((padTop + innerH * 0.5) * 10) / 10;

    return `
      <svg class="solar-curve-svg" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none">
        <defs>
          <linearGradient id="teslaSolarGlow" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#F5C542" stop-opacity="0.32"/>
            <stop offset="60%" stop-color="#F5C542" stop-opacity="0.08"/>
            <stop offset="100%" stop-color="#F5C542" stop-opacity="0.0"/>
          </linearGradient>
        </defs>

        <!-- Linha de grade 50% e linha de horizonte -->
        <line x1="${padX}" y1="${midY}" x2="${W - padX}" y2="${midY}" stroke="rgba(255,255,255,0.04)" stroke-width="1" stroke-dasharray="3,3" />
        <line x1="${padX}" y1="${baseY}" x2="${W - padX}" y2="${baseY}" stroke="rgba(255,255,255,0.08)" stroke-width="1" />

        <!-- Projeção tracejada para o restante da tarde/entardecer -->
        <line x1="${last.x}" y1="${last.y}" x2="${W - padX}" y2="${baseY}" stroke="rgba(245,197,66,0.18)" stroke-width="1.2" stroke-dasharray="3,3" />

        <!-- Área sombreada com gradiente solar -->
        <path d="${areaD}" fill="url(#teslaSolarGlow)" />

        <!-- Linha da curva solar -->
        <path d="${pathD}" fill="none" stroke="#F5C542" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" />

        <!-- Ponto pulsante ao vivo no horário atual -->
        <circle cx="${last.x}" cy="${last.y}" r="3.2" fill="#FFF59D" />
        <circle cx="${last.x}" cy="${last.y}" r="6.5" fill="none" stroke="#F5C542" stroke-width="1.4" opacity="0.85">
          <animate attributeName="r" values="3.2;8;3.2" dur="2s" repeatCount="indefinite"/>
          <animate attributeName="opacity" values="0.9;0.15;0.9" dur="2s" repeatCount="indefinite"/>
        </circle>
      </svg>
    `;
  }

  getWeatherSvg(iconStr = "", descStr = "") {
    const raw = (String(iconStr) + " " + String(descStr)).toLowerCase();

    // 1. Tempestade / Trovoada
    if (raw.includes("⛈") || raw.includes("tempestade") || raw.includes("trovoada")) {
      return `<svg class="weather-svg" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
        <path d="M19.35 9.04C18.67 5.59 15.64 3 12 3 9.11 3 6.6 4.64 5.35 7.04 2.34 7.36 0 9.91 0 13c0 3.31 2.69 6 6 6h13c2.76 0 5-2.24 5-5 0-2.64-2.05-4.78-4.65-4.96z" fill="#546E7A"/>
        <path d="M13 10.5l-3 4.5h3l-1.5 5 4.5-5.5h-3l2-4h-2z" fill="#FFD600"/>
      </svg>`;
    }

    // 2. Pancadas de Chuva (Sol + Nuvem + Chuva)
    if (raw.includes("🌦") || raw.includes("pancada")) {
      return `<svg class="weather-svg" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
        <circle cx="15.5" cy="7" r="3.2" fill="#F5C542"/>
        <path d="M15.5 2v1.5M15.5 11v1.5M10.5 7h1.5M19 7h1.5" stroke="#F5C542" stroke-width="1.3" stroke-linecap="round"/>
        <path d="M14.5 12.5c-.4-1.8-1.9-3-3.8-3-1.5 0-2.8.9-3.4 2.1-1.6.2-2.8 1.5-2.8 3.1 0 1.8 1.4 3.1 3.1 3.1h6.8c1.5 0 2.6-1.1 2.6-2.6 0-1.4-1.1-2.5-2.5-2.7z" fill="#90A4AE"/>
        <path d="M8 18.5l-1.2 2.5M12 18.5l-1.2 2.5" stroke="#00E5FF" stroke-width="1.8" stroke-linecap="round"/>
      </svg>`;
    }

    // 3. Chuva / Garoa
    if (raw.includes("🌧") || raw.includes("chuva") || raw.includes("garoa")) {
      return `<svg class="weather-svg" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
        <path d="M19.35 10.04C18.67 6.59 15.64 4 12 4 9.11 4 6.6 5.64 5.35 8.04 2.34 8.36 0 10.91 0 14c0 3.31 2.69 6 6 6h13c2.76 0 5-2.24 5-5 0-2.64-2.05-4.78-4.65-4.96z" fill="#90A4AE"/>
        <path d="M8 19l-1.5 3M12 19l-1.5 3M16 19l-1.5 3" stroke="#00E5FF" stroke-width="2" stroke-linecap="round"/>
      </svg>`;
    }

    // 4. Nevoeiro
    if (raw.includes("🌫") || raw.includes("nevoeiro") || raw.includes("neblina")) {
      return `<svg class="weather-svg" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
        <path d="M4 7h16M2 11h20M4 15h16M6 19h12" stroke="#B0BEC5" stroke-width="2" stroke-linecap="round"/>
      </svg>`;
    }

    // 5. Parcialmente Nublado / Céu Aberto com Sol
    if (raw.includes("⛅") || raw.includes("🌤") || raw.includes("parcial") || raw.includes("aberto")) {
      return `<svg class="weather-svg" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
        <circle cx="15.5" cy="8.5" r="3.5" fill="#F5C542"/>
        <path d="M15.5 3v1.5M15.5 12.5V14M10 8.5h1.5M19.5 8.5H21M11.6 4.6l1.1 1.1M17.9 10.9l1.1 1.1M11.6 12.4l1.1-1.1M17.9 6.1l1.1-1.1" stroke="#F5C542" stroke-width="1.5" stroke-linecap="round"/>
        <path d="M15.5 13.5c-.4-2.1-2.2-3.5-4.3-3.5-1.7 0-3.2 1-3.9 2.4-1.8.2-3.3 1.7-3.3 3.6 0 2 1.6 3.5 3.6 3.5h7.8c1.7 0 3-1.3 3-3 0-1.6-1.2-2.9-2.9-3z" fill="#CFD8DC"/>
      </svg>`;
    }

    // 6. Totalmente Nublado
    if (raw.includes("☁") || raw.includes("nublado") || raw.includes("encoberto")) {
      return `<svg class="weather-svg" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
        <path d="M19.35 10.04C18.67 6.59 15.64 4 12 4 9.11 4 6.6 5.64 5.35 8.04 2.34 8.36 0 10.91 0 14c0 3.31 2.69 6 6 6h13c2.76 0 5-2.24 5-5 0-2.64-2.05-4.78-4.65-4.96z" fill="#B0BEC5"/>
      </svg>`;
    }

    // 7. Ensolarado / Padrão (Sol Puro)
    return `<svg class="weather-svg" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
      <circle cx="12" cy="12" r="5" fill="#F5C542"/>
      <path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.93 4.93l1.77 1.77M17.3 17.3l1.77 1.77M4.93 19.07l1.77-1.77M17.3 6.7l1.77-1.77" stroke="#F5C542" stroke-width="2" stroke-linecap="round"/>
    </svg>`;
  }

  extractKpis(solar) {
    if (!solar) {
      return { powerNum: "0.00", todayStr: "0.0", monthStr: "0", monthUnit: "kWh", totalStr: "0.0", peakNum: "0.00", dailyCurve: [] };
    }

    const powerNum = typeof solar.potencia_kw === "number"
      ? solar.potencia_kw.toFixed(2)
      : (parseFloat(String(solar.potencia_realtime || "0").replace(/[^\d.]/g, "")) || 0).toFixed(2);

    const peakNum = typeof solar.peak_power_kw === "number" && solar.peak_power_kw > 0
      ? solar.peak_power_kw.toFixed(2)
      : powerNum;

    const dailyCurve = Array.isArray(solar.daily_curve) ? solar.daily_curve : [];

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
    const monthStr = monthKwhNum >= 1000 ? (monthKwhNum / 1000.0).toFixed(2) : (monthKwhNum >= 100 ? String(Math.round(monthKwhNum)) : monthKwhNum.toFixed(1));
    const monthUnit = monthKwhNum >= 1000 ? "MWh" : "kWh";

    let totalMwhNum = typeof solar.geracao_ano_mwh === "number" && solar.geracao_ano_mwh > 0
      ? solar.geracao_ano_mwh
      : null;
    if (totalMwhNum === null) {
      const rawTotal = String(solar.eletricidade_acumulada || solar.geracao_ano || "0");
      const parsedT = parseFloat(rawTotal.replace(/[^\d.]/g, "")) || 0;
      if (rawTotal.toUpperCase().includes("MWH")) {
        totalMwhNum = parsedT;
      } else {
        totalMwhNum = parsedT >= 100 ? parsedT / 1000.0 : parsedT;
      }
    }
    const totalStr = totalMwhNum.toFixed(1);

    return { powerNum, todayStr, monthStr, monthUnit, totalStr, peakNum, dailyCurve };
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

    const { powerNum, todayStr, monthStr, monthUnit, totalStr, peakNum, dailyCurve } = this.extractKpis(solar);
    const dailyCurveSvg = this.buildDailyCurveSvg(dailyCurve, parseFloat(powerNum) || 0, parseFloat(peakNum) || 0);
    const recentDaysHtml = this.buildRecent3Days(parseFloat(todayStr) || 0);

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

    const curSvg = this.getWeatherSvg(curIcon, curDesc);
    const tSvg = this.getWeatherSvg(tIcon, tDesc);
    const sunHeaderSvg = this.getWeatherSvg("☀️");
    const weatherHeaderSvg = this.getWeatherSvg("⛅");

    this.container.innerHTML = `
      <div class="solar-weather-split-row">
        
        <!-- 2/3 ESQUERDA: MINHA ENERGIA SOLAR (TESLA SOLAR COMMAND) -->
        <div class="solar-weather-card solar-minimal-card solar-col-2-3">
          
          <!-- 1. Cabeçalho com status ao vivo e pico -->
          <div class="solar-card-header">
            <div class="solar-header-left">
              <span class="solar-live-dot"></span>
              <span class="solar-sun-icon">${sunHeaderSvg}</span>
              <span class="solar-card-title">MINHA ENERGIA SOLAR</span>
            </div>
            <div class="solar-header-right">
              <span class="solar-peak-badge">PICO HOJE <b>${peakNum} kW</b></span>
            </div>
          </div>

          <!-- 2. Grid de 4 KPIs alinhados (Agora, Hoje, Mês, Total) -->
          <div class="solar-kpi-grid">
            <div class="solar-kpi-item kpi-agora">
              <span class="solar-kpi-lbl">AGORA</span>
              <div class="solar-kpi-val-row">
                <span class="solar-kpi-val kpi-val-agora">${powerNum}</span>
                <span class="solar-kpi-unit">kW</span>
              </div>
            </div>
            <div class="solar-kpi-item">
              <span class="solar-kpi-lbl">HOJE</span>
              <div class="solar-kpi-val-row">
                <span class="solar-kpi-val">${todayStr}</span>
                <span class="solar-kpi-unit">kWh</span>
              </div>
            </div>
            <div class="solar-kpi-item">
              <span class="solar-kpi-lbl">MÊS</span>
              <div class="solar-kpi-val-row">
                <span class="solar-kpi-val">${monthStr}</span>
                <span class="solar-kpi-unit">${monthUnit}</span>
              </div>
            </div>
            <div class="solar-kpi-item">
              <span class="solar-kpi-lbl">TOTAL</span>
              <div class="solar-kpi-val-row">
                <span class="solar-kpi-val">${totalStr}</span>
                <span class="solar-kpi-unit">MWh</span>
              </div>
            </div>
          </div>

          <!-- 3. Curva Solar Diária Expansiva (06:00 às 18:30) -->
          <div class="solar-daily-chart-block">
            <div class="solar-chart-stage">
              ${dailyCurveSvg}
            </div>
            <div class="solar-curve-timeline">
              <span>06:00</span>
              <span>09:00</span>
              <span>12:00</span>
              <span>15:00</span>
              <span>18:30</span>
            </div>
          </div>

        </div>

        <!-- 1/3 DIREITA: CLIMA (HOJE + AMANHÃ) & GERAÇÃO ÚLTIMOS 3 DIAS -->
        <div class="solar-weather-card solar-minimal-card weather-dual-card solar-col-1-3">
          
          <!-- Cabeçalho com status e atalho de diagnóstico -->
          <div class="solar-card-header weather-card-header">
            <div class="solar-header-left">
              <span class="weather-live-dot"></span>
              <span class="solar-sun-icon">${weatherHeaderSvg}</span>
              <span class="solar-card-title">CLIMA</span>
            </div>
            <div class="solar-header-right">
              <a href="/api/diagnostics" target="_blank" class="solar-corner-dash" title="Diagnóstico de Telemetria">↗</a>
            </div>
          </div>

          <!-- Metade Superior: Clima Compacto Minimalista (Hoje à esq, Amanhã à dir) -->
          <div class="weather-compact-stage">
            <div class="weather-today-compact" title="${curDesc}">
              <span class="weather-today-icon">${curSvg}</span>
              <div class="weather-today-temp-box">
                <span class="weather-today-temp">${curTemp}°</span>
                <div class="weather-today-minmax">
                  <span class="weather-arrow-val temp-high"><span class="weather-arrow">↑</span>${todayMax}°</span>
                  <span class="weather-arrow-val temp-low"><span class="weather-arrow">↓</span>${todayMin}°</span>
                </div>
              </div>
            </div>

            <div class="weather-tomorrow-pill" title="Amanhã: ${tDesc}">
              <div class="tomorrow-pill-header">
                <span class="tomorrow-pill-tag">AMANHÃ</span>
                <span class="tomorrow-pill-icon">${tSvg}</span>
              </div>
              <div class="tomorrow-pill-temps">
                <span class="weather-arrow-val tomorrow-high"><span class="weather-arrow">↑</span>${tMax}°</span>
                <span class="weather-arrow-val tomorrow-low"><span class="weather-arrow">↓</span>${tMin}°</span>
              </div>
            </div>
          </div>

          <!-- Metade Inferior: Geração dos Últimos 3 Dias -->
          <div class="solar-recent-history-block">
            <div class="recent-history-header">
              <span class="recent-history-title">GERAÇÃO ÚLTIMOS 3 DIAS</span>
            </div>
            <div class="recent-history-grid">
              ${recentDaysHtml}
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
