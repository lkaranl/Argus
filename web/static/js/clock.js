/**
 * ARGUS CCTV COMMAND HUB — Módulo de Relógio Widescreen 2/3 + Mini Calendário com Feriados 1/3
 *
 * Arquitetura de Ultra Baixo Consumo:
 * - 2/3 Esquerda: Relógio Unificado Monumental com Horas, Minutos, Segundos integrados no mesmo bloco + Data & Período do Dia.
 * - 1/3 Direita : Mini Calendário Mensal compacto destacando o dia atual e FERIADOS nacionais (fixos e móveis).
 * - Zero animações contínuas de alto consumo; recálculo do calendário ocorre 1x por dia.
 */

class ArgusModernClock {
  constructor(containerId = "cctv-clock-widget") {
    this.container = document.getElementById(containerId);
    if (!this.container) return;

    this.diasSemanaLongos = [
      "DOMINGO",
      "SEGUNDA-FEIRA",
      "TERÇA-FEIRA",
      "QUARTA-FEIRA",
      "QUINTA-FEIRA",
      "SEXTA-FEIRA",
      "SÁBADO"
    ];

    this.meses = [
      "JANEIRO", "FEVEREIRO", "MARÇO", "ABRIL", "MAIO", "JUNHO",
      "JULHO", "AGOSTO", "SETEMBRO", "OUTUBRO", "NOVEMBRO", "DEZEMBRO"
    ];

    // Feriados Nacionais Brasileiros Fixos (mês 0-indexado: 0 = Jan, 1 = Fev, ...)
    this.feriadosNacionais = {
      0: { 1: "Confraternização Universal" },
      3: { 21: "Tiradentes" },
      4: { 1: "Dia do Trabalho" },
      8: { 7: "Independência do Brasil" },
      9: { 12: "Nossa Sra. Aparecida" },
      10: { 2: "Finados", 15: "Proclamação da República", 20: "Dia da Consciência Negra" },
      11: { 25: "Natal" }
    };

    this.lastH = -1;
    this.lastM = -1;
    this.lastDay = -1;
    this.lastMonth = -1;
    this.lastGreeting = "";

    this.render();
    this.start();
  }

  getHolidaysForYear(year, month) {
    const fixed = this.feriadosNacionais[month] || {};
    const res = { ...fixed };

    // Feriados Móveis: Carnaval, Sexta-Feira Santa, Páscoa, Corpus Christi
    // Algoritmo astronômico de Meeus/Jones/Butcher para Páscoa
    const a = year % 19;
    const b = Math.floor(year / 100);
    const c = year % 100;
    const d = Math.floor(b / 4);
    const e = b % 4;
    const f = Math.floor((b + 8) / 25);
    const g = Math.floor((b - f + 1) / 3);
    const h = (19 * a + b - d - g + 15) % 30;
    const i = Math.floor(c / 4);
    const k = c % 4;
    const l = (32 + 2 * e + 2 * i - h - k) % 7;
    const m = Math.floor((a + 11 * h + 22 * l) / 451);
    const pascoaMonth = Math.floor((h + l - 7 * m + 114) / 31) - 1; // 0-indexado
    const pascoaDay = ((h + l - 7 * m + 114) % 31) + 1;

    const pascoaDate = new Date(year, pascoaMonth, pascoaDay);

    // Carnaval: Páscoa - 47 dias
    const carnaval = new Date(pascoaDate);
    carnaval.setDate(carnaval.getDate() - 47);
    if (carnaval.getMonth() === month) res[carnaval.getDate()] = "Carnaval";

    // Sexta-feira Santa: Páscoa - 2 dias
    const sextaSanta = new Date(pascoaDate);
    sextaSanta.setDate(sextaSanta.getDate() - 2);
    if (sextaSanta.getMonth() === month) res[sextaSanta.getDate()] = "Sexta-feira Santa";

    // Corpus Christi: Páscoa + 60 dias
    const corpus = new Date(pascoaDate);
    corpus.setDate(corpus.getDate() + 60);
    if (corpus.getMonth() === month) res[corpus.getDate()] = "Corpus Christi";

    return res;
  }

  buildCalendarHtml(now) {
    const year = now.getFullYear();
    const month = now.getMonth();
    const todayNum = now.getDate();
    const holidays = this.getHolidaysForYear(year, month);

    const firstDayIndex = new Date(year, month, 1).getDay(); // 0 = Dom, 1 = Seg...
    const totalDaysInMonth = new Date(year, month + 1, 0).getDate();

    let gridHtml = "";
    // Dias em branco antes do 1º dia
    for (let i = 0; i < firstDayIndex; i++) {
      gridHtml += `<span class="cal-day cal-empty"></span>`;
    }

    // Dias do mês
    let nextHolidayText = "";
    let closestHolidayDiff = 999;

    for (let day = 1; day <= totalDaysInMonth; day++) {
      const isToday = day === todayNum;
      const isHoliday = Boolean(holidays[day]);
      let classes = "cal-day";
      if (isToday) classes += " cal-today";
      if (isHoliday) classes += " cal-holiday";

      const titleAttr = isHoliday ? `title="${holidays[day]}"` : "";
      gridHtml += `<span class="${classes}" ${titleAttr}>${day}</span>`;

      if (isHoliday && day >= todayNum && (day - todayNum) < closestHolidayDiff) {
        closestHolidayDiff = day - todayNum;
        const diaTxt = day < 10 ? `0${day}` : day;
        nextHolidayText = `${diaTxt}/${month + 1 < 10 ? '0' + (month + 1) : month + 1} - ${holidays[day]}`;
      }
    }

    // Se não encontrou feriado no restante deste mês, busca no próximo
    if (!nextHolidayText) {
      const nextM = (month + 1) % 12;
      const nextY = nextM === 0 ? year + 1 : year;
      const nextHolidays = this.getHolidaysForYear(nextY, nextM);
      const nextDays = Object.keys(nextHolidays).map(Number).sort((a,b) => a - b);
      if (nextDays.length > 0) {
        const firstD = nextDays[0];
        const diaTxt = firstD < 10 ? `0${firstD}` : firstD;
        const mesTxt = nextM + 1 < 10 ? `0${nextM + 1}` : nextM + 1;
        nextHolidayText = `${diaTxt}/${mesTxt} - ${nextHolidays[firstD]}`;
      } else {
        nextHolidayText = "Nenhum feriado próximo";
      }
    }

    const mesAnoTitle = `${this.meses[month]} ${year}`;

    return {
      mesAnoTitle,
      gridHtml,
      nextHolidayText
    };
  }

  render() {
    this.container.innerHTML = `
      <div class="clock-calendar-split-row">
        
        <!-- 2/3 ESQUERDA: RELÓGIO UNIFICADO (HORA + SEGUNDOS + PERÍODO) -->
        <div class="apple-clock-card clock-unified-card clock-col-2-3">
          
          <!-- Topo: Dia da Semana e Data Completa -->
          <div class="clock-unified-header">
            <div class="clock-header-left">
              <span class="apple-status-beacon"></span>
              <span class="clock-weekday-txt" id="apple-weekday">--</span>
            </div>
            <span class="clock-full-date" id="apple-dial-date">--</span>
          </div>

          <!-- Centro: Hora Monumental com Segundos e Período Integrados -->
          <div class="clock-unified-center">
            <div class="clock-digits-cluster">
              <span class="apple-digit" id="apple-hours">--</span>
              <span class="apple-colon">:</span>
              <span class="apple-digit" id="apple-minutes">--</span>
            </div>
            
            <div class="clock-badge-cluster">
              <div class="clock-inline-seconds">
                <span class="clock-sec-val mono" id="apple-seconds-pill">00</span>
                <span class="clock-sec-lbl">SEG</span>
              </div>
              <span class="clock-inline-period" id="apple-greeting-tag">MANHÃ</span>
            </div>
          </div>

        </div>

        <!-- 1/3 DIREITA: MINI CALENDÁRIO COM DESTAQUE DE FERIADOS -->
        <div class="apple-clock-card calendar-mini-card clock-col-1-3">
          
          <div class="cal-header">
            <span class="cal-title" id="cal-month-year">OUTUBRO 2026</span>
            <span class="cal-holiday-tag" title="Feriados destacados em dourado">● FERIADOS</span>
          </div>

          <div class="cal-weekdays-row">
            <span>D</span><span>S</span><span>T</span><span>Q</span><span>Q</span><span>S</span><span>S</span>
          </div>

          <div class="cal-grid" id="cal-days-grid">
            <!-- Injetado dinamicamente -->
          </div>

          <div class="cal-next-holiday" id="cal-next-holiday" title="Próximo Feriado">
            Próximo feriado: <b id="cal-holiday-text">Carregando...</b>
          </div>

        </div>

      </div>
    `;

    this.elHours = document.getElementById("apple-hours");
    this.elMinutes = document.getElementById("apple-minutes");
    this.elSecPill = document.getElementById("apple-seconds-pill");
    this.elWeekday = document.getElementById("apple-weekday");
    this.elDate = document.getElementById("apple-dial-date");
    this.elGreeting = document.getElementById("apple-greeting-tag");

    this.elCalTitle = document.getElementById("cal-month-year");
    this.elCalGrid = document.getElementById("cal-days-grid");
    this.elCalHoliday = document.getElementById("cal-holiday-text");
  }

  updateCalendar(now) {
    const data = this.buildCalendarHtml(now);
    if (this.elCalTitle) this.elCalTitle.textContent = data.mesAnoTitle;
    if (this.elCalGrid) this.elCalGrid.innerHTML = data.gridHtml;
    if (this.elCalHoliday) this.elCalHoliday.textContent = data.nextHolidayText;
  }

  start() {
    const updateNow = () => {
      const now = new Date();
      const h = now.getHours();
      const m = now.getMinutes();
      const s = now.getSeconds();
      const dayNum = now.getDate();
      const monthNum = now.getMonth();

      // Segundos atualizam todo segundo (apenas nó de texto simples)
      if (this.elSecPill) {
        this.elSecPill.textContent = String(s).padStart(2, "0");
      }

      // Horas (1x por hora)
      if (h !== this.lastH) {
        this.lastH = h;
        if (this.elHours) this.elHours.textContent = String(h).padStart(2, "0");

        // Saudação por período
        let greeting = "MANHÃ";
        if (h >= 12 && h < 18) greeting = "TARDE";
        else if (h >= 18 && h <= 23) greeting = "NOITE";
        else if (h >= 0 && h < 6) greeting = "MADRUGADA";

        if (greeting !== this.lastGreeting) {
          this.lastGreeting = greeting;
          if (this.elGreeting) this.elGreeting.textContent = greeting;
        }
      }

      // Minutos (1x por minuto)
      if (m !== this.lastM) {
        this.lastM = m;
        if (this.elMinutes) this.elMinutes.textContent = String(m).padStart(2, "0");
      }

      // Calendário e data (apenas 1x quando vira o dia)
      if (dayNum !== this.lastDay || monthNum !== this.lastMonth) {
        this.lastDay = dayNum;
        this.lastMonth = monthNum;

        const weekday = this.diasSemanaLongos[now.getDay()];
        const fullDate = `${dayNum} DE ${this.meses[monthNum]}`;

        if (this.elWeekday) this.elWeekday.textContent = weekday;
        if (this.elDate) this.elDate.textContent = fullDate;

        this.updateCalendar(now);
      }
    };

    updateNow();
    setInterval(updateNow, 1000);
  }
}

document.addEventListener("DOMContentLoaded", () => {
  window.argusModernClock = new ArgusModernClock("cctv-clock-widget");
});
