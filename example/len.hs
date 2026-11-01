#!/usr/bin/env runghc
-- Haskell version of len.ent
main :: IO ()
main = do
  let name = "world"
  let langs = ["go", "bash", "nix"]
  let n = length langs
  putStrLn ("hello " ++ name ++ ", " ++ show n ++ " langs") -- hello world, 3 langs

  let n = (length langs)
  putStrLn ("hello " ++ name ++ ", " ++ show n ++ " langs") -- hello world, 3 langs

  let n = length langs + 1 -- no parens needed: application binds tighter than +
  putStrLn ("hello " ++ name ++ ", " ++ show n ++ " langs") -- hello world, 4 langs
