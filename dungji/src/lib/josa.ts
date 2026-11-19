/** 받침이 있으면 a, 없으면 b 를 붙인다: josa("튤립", "이", "가") → "튤립이" */
export function josa(word: string, a: string, b: string) {
  const code = word.charCodeAt(word.length - 1);
  const hangul = code >= 0xac00 && code <= 0xd7a3;
  return word + (hangul && (code - 0xac00) % 28 !== 0 ? a : b);
}
