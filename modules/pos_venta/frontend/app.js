// Lógica de tabs para POS Venta
document.querySelectorAll('.ft-tab-btn').forEach(btn => {
  btn.addEventListener('click', () => {
    const tabName = btn.dataset.tab;

    // Desactivar todos
    document.querySelectorAll('.ft-tab-btn').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.ft-tab-content').forEach(c => c.classList.remove('active'));

    // Activar seleccionado
    btn.classList.add('active');
    document.getElementById(tabName).classList.add('active');
  });
});
