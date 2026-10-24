/**
 * ARGUS CCTV COMMAND HUB — Relógio Modular Tático (Estilo Apple Watch Ultra)
 * 60 Marcadores de Segundos (Bezel Ticks) com motor vetorial SVG de precisão.
 */

class AppleModularClock {
  constructor(containerId = "cctv-clock-widget") {
    this.container = document.getElementById(containerId);
    if (!this.container) return;

    this.diasSemana = ["DOM", "SEG", "TER", "QUA", "QUI", "SEX", "SÁB"];
    this.meses = ["JAN", "FEV", "MAR", "ABR", "MAI", "JUN", "JUL", "AGO", "SET", "OUT", "NOV", "DEZ"];

    this.width = 560;
    this.height = 240;
    this.tickElements = [];

    this.render();
    this.syncDimensionsAndBuildTicks();
    this.start();

    window.addEventListener("resize", () => {
      this.syncDimensionsAndBuildTicks();
    });
  }

  render() {
    this.container.innerHTML = `
      <div class="apple-clock-card" id="apple-clock-card">
        <!-- Bezel de 60 Segundos em SVG de Alta Precisão -->
        <svg class="apple-bezel-svg" id="apple-bezel-svg" viewBox="0 0 ${this.width} ${this.height}" preserveAspectRatio="none">
          <rect 
            id="apple-bezel-guide"
            x="14" y="14" 
            width="${this.width - 28}" 
            height="${this.height - 28}" 
            rx="22" ry="22"
            fill="none" 
            stroke="none"
          />
          <g id="apple-ticks-group"></g>
        </svg>

        <!-- Conteúdo Interno do Mostrador -->
        <div class="apple-dial-content">
          <div class="apple-dial-header">
            <span class="apple-dial-date" id="apple-dial-date">--</span>
            <div class="apple-live-status">
              <span class="apple-status-beacon"></span>
              <span class="apple-status-text" id="apple-greeting-tag">ONLINE</span>
            </div>
          </div>

          <div class="apple-time-display">
            <div class="apple-digits-wrap">
              <span class="apple-digit" id="apple-hours">00</span>
              <span class="apple-colon">:</span>
              <span class="apple-digit" id="apple-minutes">00</span>
            </div>
            <span class="apple-seconds-pill" id="apple-seconds-pill">00</span>
          </div>
        </div>
      </div>
    `;

    this.elCard = document.getElementById("apple-clock-card");
    this.elSvg = document.getElementById("apple-bezel-svg");
    this.elGuide = document.getElementById("apple-bezel-guide");
    this.elHours = document.getElementById("apple-hours");
    this.elMinutes = document.getElementById("apple-minutes");
    this.elSecPill = document.getElementById("apple-seconds-pill");
    this.elDate = document.getElementById("apple-dial-date");
    this.elGreeting = document.getElementById("apple-greeting-tag");
    this.ticksGroup = document.getElementById("apple-ticks-group");
  }

  syncDimensionsAndBuildTicks() {
    if (this.elCard) {
      const rect = this.elCard.getBoundingClientRect();
      if (rect.width > 100 && rect.height > 80) {
        this.width = Math.round(rect.width);
        this.height = Math.round(rect.height);
      }
    }
    if (this.elSvg && this.elGuide) {
      this.elSvg.setAttribute("viewBox", `0 0 ${this.width} ${this.height}`);
      this.elGuide.setAttribute("width", String(this.width - 28));
      this.elGuide.setAttribute("height", String(this.height - 28));
    }
    this.buildBezelTicks();
  }

  buildBezelTicks() {
    const guide = this.elGuide || document.getElementById("apple-bezel-guide");
    if (!guide || !this.ticksGroup) return;

    this.ticksGroup.innerHTML = "";
    const totalLen = guide.getTotalLength();
    this.tickElements = [];

    const rx = 22;
    const topStraightWidth = this.width - 28 - (2 * rx);
    const topCenterOffset = topStraightWidth / 2;

    for (let i = 0; i < 60; i++) {
      const frac = i / 60;
      let dist = (frac * totalLen) + topCenterOffset;
      if (dist >= totalLen) dist -= totalLen;

      const p1 = guide.getPointAtLength(dist);
      const nextDist = dist + 1 > totalLen ? dist + 1 - totalLen : dist + 1;
      const p2 = guide.getPointAtLength(nextDist);

      const dx = p2.x - p1.x;
      const dy = p2.y - p1.y;
      const len = Math.sqrt(dx * dx + dy * dy) || 1;

      const nx = -dy / len;
      const ny = dx / len;

      const isMajor = i % 5 === 0;
      const tickLength = isMajor ? 10 : 6;
      const strokeWidth = isMajor ? 2.5 : 1.5;

      const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
      line.setAttribute("x1", p1.x.toFixed(2));
      line.setAttribute("y1", p1.y.toFixed(2));
      line.setAttribute("x2", (p1.x + nx * tickLength).toFixed(2));
      line.setAttribute("y2", (p1.y + ny * tickLength).toFixed(2));
      line.setAttribute("stroke-width", strokeWidth);
      line.setAttribute("stroke-linecap", "round");
      line.setAttribute("class", `apple-tick ${isMajor ? 'major' : 'minor'}`);

      this.ticksGroup.appendChild(line);
      this.tickElements.push(line);
    }
  }

  start() {
    const update = () => {
      const now = new Date();
      const h = now.getHours();
      const m = now.getMinutes();
      const s = now.getSeconds();

      if (this.elHours) this.elHours.textContent = String(h).padStart(2, "0");
      if (this.elMinutes) this.elMinutes.textContent = String(m).padStart(2, "0");
      if (this.elSecPill) this.elSecPill.textContent = String(s).padStart(2, "0");

      if (this.elDate) {
        const diaSem = this.diasSemana[now.getDay()];
        const diaNum = now.getDate();
        const mesStr = this.meses[now.getMonth()];
        this.elDate.textContent = `${diaSem}, ${diaNum} ${mesStr}`;
      }

      if (this.elGreeting) {
        if (h >= 5 && h < 12) this.elGreeting.textContent = "MANHÃ";
        else if (h >= 12 && h < 18) this.elGreeting.textContent = "TARDE";
        else this.elGreeting.textContent = "NOITE";
      }

      for (let i = 0; i < 60; i++) {
        const tick = this.tickElements[i];
        if (!tick) continue;

        if (i <= s) {
          tick.classList.add("active");
          if (i === s) {
            tick.classList.add("head");
          } else {
            tick.classList.remove("head");
          }
        } else {
          tick.classList.remove("active", "head");
        }
      }
    };

    update();
    setInterval(update, 1000);
  }
}

document.addEventListener("DOMContentLoaded", () => {
  window.argusClock = new AppleModularClock("cctv-clock-widget");
});
