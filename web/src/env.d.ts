declare module '$env/static/public' {
  export const PUBLIC_GRAPHQL_URL: string;
  export const PUBLIC_WS_URL: string;
}

declare module '*.html?raw' {
  const content: string;
  export default content;
}
