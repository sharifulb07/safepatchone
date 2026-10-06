import AuthPage from "../components/auth";
export const metadata = { title: "Sign in · SafePatch", robots: { index: false, follow: false } };
export default function SignIn() { return <AuthPage signup={false} />; }
