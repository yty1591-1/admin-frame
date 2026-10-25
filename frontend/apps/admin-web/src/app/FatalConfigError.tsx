export function FatalConfigError({ message }: { message: string }) {
  return (
    <main style={{ padding: 48, fontFamily: 'system-ui, sans-serif' }}>
      <h1 style={{ fontSize: 20 }}>前端配置加载失败</h1>
      <p>{message}</p>
      <p style={{ color: '#666' }}>请联系管理员检查 /config.js 与构建期端别配置。</p>
    </main>
  );
}
