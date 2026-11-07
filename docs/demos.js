// Replay terminal cells captured by record-demos.py. The UI is entish's output.
(() => {
  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const narrow = window.matchMedia('(max-width: 640px)');
  document.querySelectorAll('[data-demo]').forEach(element => {
    const variants = window.entishRecordings?.[element.dataset.demo];
    if (!variants) return; // Leave the captured static fallback if assets fail to load.
    const screen = element.querySelector('.demo-screen');
    let recording;
    let index;
    let visible = false;
    let timer;
    const measure = document.createElement('canvas').getContext('2d');

    function fit() {
      // Keep the captured terminal's column count, fitting its cells to the panel.
      const style = getComputedStyle(screen);
      measure.font = `100px ${style.fontFamily}`;
      const cellWidth = measure.measureText('0').width / 100;
      const width = screen.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
      screen.style.fontSize = `${Math.floor(width / recording.columns / cellWidth * 1000) / 1000}px`;
      screen.style.minHeight = `calc(${Math.max(6, recording.rows) * 1.6}em + 24px)`;
    }

    function render() {
      const content = document.createDocumentFragment();
      recording.frames[index].lines.forEach((line, row) => {
        if (row) content.append(document.createTextNode('\n'));
        line.forEach(([style, text]) => {
          const span = document.createElement('span');
          span.className = style;
          span.textContent = text;
          content.append(span);
        });
      });
      screen.replaceChildren(content);
    }

    function schedule() {
      clearTimeout(timer);
      if (reducedMotion.matches || !visible || document.hidden) return;
      timer = setTimeout(() => {
        index = (index + 1) % recording.frames.length;
        render();
        schedule();
      }, recording.frames[index].ms);
    }

    function reset() {
      recording = variants[narrow.matches ? 'narrow' : 'wide'];
      index = reducedMotion.matches ? recording.poster : 0;
      fit();
      render();
      schedule();
    }

    reducedMotion.addEventListener('change', reset);
    narrow.addEventListener('change', reset);
    document.addEventListener('visibilitychange', schedule);
    new IntersectionObserver(entries => {
      visible = entries[0].isIntersecting;
      schedule();
    }).observe(element);
    reset();
    new ResizeObserver(fit).observe(element);
    document.fonts.ready.then(fit);
  });
})();
