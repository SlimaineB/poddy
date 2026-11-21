(function () {
  const key = "poddy.ideReload:" + location.pathname;
  const lastReload = () => {
    try {
      return Number(sessionStorage.getItem(key) || 0);
    } catch {
      return 0;
    }
  };
  const markReload = () => {
    try {
      sessionStorage.setItem(key, String(Date.now()));
    } catch {}
  };
  const rendered = () => Boolean(document.querySelector(".monaco-workbench, .monaco-editor, #workbench\.parts\.editor"));
  const check = (attempt) => {
    if (rendered()) return;
    if (document.readyState !== "complete" && attempt < 5) {
      setTimeout(() => check(attempt + 1), 3000);
      return;
    }
    if (Date.now() - lastReload() < 60000) return;
    markReload();
    location.reload();
  };
  setTimeout(() => check(0), 12000);
})();
