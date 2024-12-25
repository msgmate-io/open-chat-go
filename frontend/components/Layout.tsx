import { cookies } from 'next/headers'

async function getServerSideTheme(){
  const cookieStore = await cookies()
  const themeStore = cookieStore.get('theme-store')
  const theme = themeStore?.value ? JSON.parse(themeStore?.value).state.theme : "dark"
  return theme
}

export async function Layout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  const theme = await getServerSideTheme();
  
  return (
    <html lang="en" data-theme={theme}>
      <body>
      {children}
      </body>
    </html>
  );
}