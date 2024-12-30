---
# charset() Bug Fix
## Problem I Faced
When I tried to create a vocabulary using the UTF-16 format, an unknown error occurred.  
However, when I tried without specifying any UTF options (e.g., UTF-8, UTF-16), it worked perfectly.

## Cause
The `charset` function in the code is supposed to return integer values ranging from 0 to 2.  
If the function returns any other value, it should trigger an error.  
However, it was returning what seemed to be a memory address instead.

## How I Fixed It
I changed the code to use `self.charset()`.  
Now, it successfully returns the expected data.
---
# charset() 버그 수정

## 발생한 문제
UTF-16 형식으로 단어집을 생성하려고 시도했을 때 알 수 없는 오류가 발생했습니다.  
그러나 UTF 옵션(예: UTF-8, UTF-16)을 지정하지 않고 시도했을 때는 정상적으로 작동했습니다.

## 원인
코드 내의 `charset` 함수는 0부터 2까지의 정수값을 반환해야 했습니다.  
다른 값이 반환되면 오류가 발생해야 하지만,  
실제로는 메모리 주소로 추정되는 값이 반환되고 있었습니다.

## 해결 방법
`self.charset()`을 사용하도록 코드를 수정했습니다.  
이제 함수가 기대했던 데이터를 정상적으로 반환합니다.
