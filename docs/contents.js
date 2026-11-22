// Mark the section crossing the top of the reading area.
const contentsLinks = [...document.querySelectorAll('.contents a')];
const sections = contentsLinks.map(link => document.getElementById(link.hash.slice(1)));

function updateContents() {
  const atEnd = Math.ceil(window.scrollY + window.innerHeight) >= document.documentElement.scrollHeight;
  const current = atEnd ? sections.length - 1 : sections.findLastIndex(section => section.getBoundingClientRect().top <= window.innerHeight * 0.3);
  contentsLinks.forEach((link, index) => {
    if (index === current) link.setAttribute('aria-current', 'location');
    else link.removeAttribute('aria-current');
  });
}

window.addEventListener('scroll', updateContents, { passive: true });
window.addEventListener('resize', updateContents);
updateContents();
