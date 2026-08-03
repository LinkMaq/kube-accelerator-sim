(() => {
  const requested = new URLSearchParams(location.search).get('theme')
  const system = matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
  const theme = requested === 'light' || requested === 'dark' ? requested : system
  document.documentElement.dataset.theme = theme
  document.documentElement.style.colorScheme = theme
})()
