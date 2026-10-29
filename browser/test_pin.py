"""pin 의 쿠페이 그림 키패드 읽기 - 숫자를 확실히 읽을 때만 돌려주는가.

    python -m unittest test_pin   (Pillow·websockets·aiohttp 가 있는 곳 - 브라우저 이미지)

SPRITE 는 2026-10-18 01:53 결제 화면(rocketpay.coupang.com)의 실제 스프라이트다. 키 배치만 담긴
그림이라 비밀번호와는 상관이 없다. 칸 순서(행 우선)로 5 9 8 4 / 1 2 6 7 / 0 3 이 그려져 있다.
"""

import asyncio
import base64
import io
import json
import os
import unittest
import urllib.request

os.environ.setdefault("PAYMENT_HOSTS_FILE", os.path.join(os.path.dirname(__file__), "payment-hosts.json"))

from PIL import Image  # noqa: E402

import pin  # noqa: E402

SPRITE = (
    "iVBORw0KGgoAAAANSUhEUgAAAGgAAABOCAYAAADM6VOKAAAHSUlEQVR42u1dCWxVRRSlLV2tW2kREA0talgs1QRFKTUqwURQSFRc"
    "EqWIRoJGEjGhUsRowGhAZasClqKCIoUoBlwQo0TQqMRKqAZiEalEXIASlsKHAtY7yX3m5LXz+v8sr/9/5yYnP31/3r1/5r6Zd7eZ"
    "duniyJEjR44cOXLkKMGpNUoyIOc8wlOEbwmHCScJewg1hEGG+nIlYT5hG8s4w5/b+PpAC+P3gslx6hQF0e3FhMYA9i2ERzRlzGCF"
    "BJH4fobBsbvWL9OqgizN0DzC7yBmGeFyQgZ/vsnXzxJuUZTxAPA/QZhOKCJkE/oSnuEZ69H9BvqVRdhheqXpDAXNBBEfStp8wt/v"
    "JKQpyPgBZDwqaTMZ2tQZ6NcceOASWkE/gohhkjY3QJubFGTg7OguadMD2kQ0+zSUZ/wfhAsSXUHHQURewDLo0TwFGQeiUNBF0OZv"
    "jf6IZfNn5jPG+hj6ltBvCM2Mel6e8jT5R2JU0CYFGYvh/kmSNo9Dm9c0+jOPebwbykMehQEnXvDFGvwbgFeppE0ZylOQIZaZr/h+"
    "MWMrCIX8Ii9io8F7UDYLk1+xL2Ip/oewn5AfloK2EMYTBvD0zSfcS9gNcn8l5CryfxX4rJO0+QjaHFWUkyYMBPaz2iNxfZKKEcL8"
    "z4ExuSfM14TsB+WzI+nRFEU+l7Hp61E1X0vnzxr2g/7zVTTM3uf5xd0eieuzCJmaD9rasN/jQT+qHGRv1uBzh+9d5Hcep8DfhxSj"
    "FN+BP7WQ0J+Qw59VvDR579ncGPnfzPcfEtZgPCmoJ8hu0uR1BeF1Xi6FWdwkljzC9SLUA3IaFHi/DPfPl7SpgjZzYuTvRUHKO8MS"
    "Dvph6RiOCWmmrlK4fy/c30/Spj+0+c1GOMx6VKGDGXTAopxVIGeiwv2n4f4cSZsc1YctnhU0HuR9ZknGJfB+OkY4V4HH/hhn0F+J"
    "5OzLhBaIpQBkT7C0hG4EGVMV+bwPPOZGYe6/lxAKIn4/EZ4mXEfozRHm7hwdbvRFGLpqyKkTS5fIx/BScyHhdsL3IGODho/ixcY8"
    "K26umEkSK058PyRRFBQNfYFesyU5K1T9E5AxwRc0bY/EUjouYeKZbPo+ycvMLg6THGdTuJYwmpBiQM4gDs/XsS8hZPzCofoyg/0R"
    "ju9LnH44wj7WEf5bXO9r6XXQOWa2I0eOHDly5MiRo3gkjrWN4pqytb7IQashGX0IDxKWs7n7J+d/mjgLWknoZrhfpYSlnM1tZlnC"
    "xJ+tWiTZKXG4MIRE2R9R9DHCgKxMwls2+hWjfnabUpDIMq4nPMuhl56WFNTCM2gkZ2pF+ngwYY3Pyx+gIScN6utaecaM5oKUDHZi"
    "HxOz2NJqhPVxs20ue6YVJJJyhQHfL9HJBQGfCuDzuVBKiK+KroSDIH9IwigoCnlFumkATnkfg5l4acjv8hHQh70mwmPxpKBMEHlK"
    "kcdEE7NQow9LdAov411BJSCyUZFHLfAoD1k5qaJSFeSXJZuCsCq0WpHHLuBRzIUo6/i9EOHI+SIb0WzieSPIFi5EatIoSBTT+xJt"
    "AxX5HIaf/UTAPiGR5hhluA8LTZQUx52CiH0vfuICU9VR8jrj2wwm24Pk1T30MdSHFN/ep+FJoSCupd4OokSEIVuDXzPwWi9pgz7S"
    "AkP9GAo8D6qm7ONKQVwn8LWvJLe3Js99UexBwgL9nYb68grwXBqWVWJNQWxSb/S9EwYb4PtljFtcThjqDxbW3JrQCmJv+wNfTfZt"
    "hngviFFBzQZkXgP8hJGSnrAKYl/hbZ9F9ZAlTz6abZb1BmS+CPyWh+l42VDQIp9yKizMzoYoNyoLmmVAJvpeYxJWQb4nzVqklyPl"
    "XnFiDexBEmb2GyB/n4FtnSU+sz0rFIXEQR6lVcei4x12pwN4i0BmiYExmxla7C+ZFARP9zK2sE5y4eJWztyeb2jM8ACLsV0cOXLk"
    "yJEjR44cOZKajiJ5tpoduVP8uVoWOomRdz8+bXETF0e2cHj+U8LdbvQ7HsBK8ML9JK5Ps+xrrbSeKk5g5YyFgdrB+fxs/kSH7C4N"
    "GVu5aFA4kbmEi/ncnKPAf7LTRtuBS4echiiuKPJ9XwT7PveYDqkTv4dBQdudRtoO0EgYoHckbVbaSkr5DtmLOI20HSA8O2CcpA0e"
    "01JlWH43DGQ6jbQdoC0wQFdJ2lxt4sQrCe/7gPdip5G2A4RnqxVI2hRgLsWQXO+wDK/gvN70HqFkURAe+JopaZOFySnD5rbISk7T"
    "KblKdgWdhcFKkbRJ1T0NMUBBEU5F3+m0EQczCHj24FMMNwDv2tAqY9w7KKbfUA38n3NaiSMrDpxhre0nzg+y5Acx/wzdDVwukmCx"
    "vJUj3c5ZDXh6O4rFRWzF4ljGClBQjdNK2wGyGs1mJ3Qq1zL34ipQsf1kOB8BgAWAhU4j7Q/i9A7yQZUGfR/Z/4codZoIHshhfLAE"
    "ZlTX6GZUJac6RlgpH3NeKNdpwJEjR47+F/QvIuPNzFajnM8AAAAASUVORK5CYII="
)
# 키 N 이 보여 주는 칸: 쿠페이 키패드의 배경 위치(가로 25, 세로 26 간격)와 span 크기(17×26).
OFFSETS = [(0, 0), (25, 0), (50, 0), (75, 0), (0, 26), (25, 26), (50, 26), (75, 26), (0, 52), (25, 52)]
DRAWN = [5, 9, 8, 4, 1, 2, 6, 7, 0, 3]


def cells(src, offsets=OFFSETS, w=17, h=26):
    return [{"key": str(i), "src": src, "x": x, "y": y, "w": w, "h": h} for i, (x, y) in enumerate(offsets)]


def uri(img):
    buf = io.BytesIO()
    img.save(buf, "PNG")
    return "data:image/png;base64," + base64.b64encode(buf.getvalue()).decode()


# 2026-10-30 00:07 네이버페이 결제 비밀번호 화면(pay.naver.com/authentication/pw/check)의 실제 스프라이트.
# 120×200, 칸 40×50(화면에는 반으로 줄여 20×25). 칸 순서(행 우선)로 0 2 6 / 1 3 7 / 8 5 9 / _ 4 _ 이
# 그려져 있다(그 화면 사진과 대조함). 키 배치만 담긴 그림이라 비밀번호와는 상관이 없다.
NAVER_SPRITE = (
    "iVBORw0KGgoAAAANSUhEUgAAAHgAAADICAYAAAAjriWnAAASpUlEQVR42u1dC7hPVRb/X7dLuK7nIEQlhqKEkmi66qaoq4RqUJcS"
    "k+QZqYhquuGWSVGUx4QhE5VC6IHSKD1QppEUTdNjSo1SIXRmr++uO3M7s9c+5+y9z/mfM9bv+9b39emetddZv//Zj7XXXjuVYjAY"
    "DAaDwWAwGBHDcZyaQjoK6SvkNiE3C+ktJFdIxTTbdoyQy4RcL2SskGFCOgs5QUiZmPivmZB8IQOE3C5koJA8IccKyUiXUWWE9BKy"
    "Usghh8b3QuYL6RChbdlCRgp5z1Fjn5BNQnpG7UjRXmMhhUJ2eNj4o5BlQlpEaVxDIeud4JgtJCdk2wqEfKNh21+EnBGB77LwKz0Q"
    "0L6fhSwA34dt4IX4Vepil5DjQrDrKCEzHTOAE2cIyQzJd7WFbDG08SchXcMit7UhuSXYJqSaZdsedexhju0uW+irYoHc0t122zAm"
    "K/9UNArd4jwhd+DYshjHOAov25rkCD03Kto5LGQDft03CbkLbfu7hxMHWu6WX/Vobwv6bwLauhaJpLBbSCObBM9WdGvjZLNlnF3P"
    "VRjZ14Jd1YT8i9C/iRpXxb+XFTJKyF7FD7aGJd+NUPjgBSEtiedqCSlSjNdbbJHbAr8EGXr5eH4S8eynpsso7DFkWA7jso/nmyu+"
    "lBstjbvfEvon+OnFcAlFkdzaBsFLCeUP+Xw+U9FFjTC0bQvx9dUJoKMvYdsaC76bTuheGFDPNYSeB0wNrEr8evYFWfLApIAw8G3D"
    "r0OGsRq6vpLo+dLQd0cL+U6iF77o7IC6Mok1M9idZWIk9ctZrKFrF6GrkaZtZxD6LtLQtYKYX2QZ+K4HYd9UTX0wnHSTSI4JwSsI"
    "I7tr6KLG4ls0bcsn9NXX0PUI0UtlGPjuacK+ZnGKM38pMRC6nfIauloSL/ykQbi0rFs0db0ksetdQ9/JZvfr40QuNcY9bqBT1k1/"
    "kOb3rECQsdhAZwPCd/08nsvA5WVOFC9+PmFkoYHOtUQwomIaCR5saxgqpbMLobO9pBeC3a0l8EMXst81RLyLa+F2Ybz4IMLIYQY6"
    "/0zobJEmcmsI+Uxiz06TmLR4dgzxng1K/c2lQj4OEKKEoEgrmy9/m25wQ6FzGqEzNw3kwtezirBnkKHuB4meKgvbLcRZus5mQz9b"
    "DphINHK+gc7xhM78NBBM2bLGNE4unn9Movcz/H+TLWw4jLHhgIcI5aca6Bxou1ewvL7/KkgUTKH/KYnu14WcS3y5h3CXbSWOxYd9"
    "bG12NTWS2iioZ6DzCkLngAjJ7UJkoYDTOltq40ViL/wfrn97DSZQEPWSzOw7eGwxfmu0IUIs/n8xUdDQ2ZPQ2ScictsrtjFHWmzn"
    "RY8vEGbLQ7yGAhyzxyq+6EkmRt5HKG1qoLMfobNbBOS2ErKHaH+i5bYWehB8kyUuIAGjnO1JSCuDFx9se+IWII67m2h7Zgjt3a8g"
    "95WgkziIHArZTujL0zVyOKHwYoMXLyR0nhkiub8W8gXR7pIwcrCEztEKgnM1dV5tNfAEkRxC4eAQuq66IZF7vGRiU4LntLs373b7"
    "KgiubNALyTDP5JcvwxSDF98o26APycn1MCIlw2r3zNVy252Jdnca6MzCQIcbq3QVZhIzzjc09ZUnkgfWheBg2Ch5XxHyK58KEUL/"
    "iUTbmw31yhITXjBR+CYRcquioSuPeOkHLTu3OgbpqShVhYiWZLI17AHdJAJMwpNhgYmRVFJbgYauqYSuThadWlnIW0Q766LctVLE"
    "8ptr6utI6JtsYuTJhNK3AurJIVJUvzbKKfplG9l4BIVammSnIgSkIhG23K2pb4btbc0SxVsJxZcG0HFXmGtQHN/XEG1ARmelVBqA"
    "udluHBRyWkA95xEx7IO6s3I/0SdI56nt4/k2aIhsLG9hwYllMUgvw4ZIsiOC+26L33kMjr0f20p+lDVQRhH0hlTOxopnz1WcPJht"
    "wbajFMltEIxvCrtDBlLF0L4MIoulJPG/s4/YPRWBgw2TJrZ+iXke52wfwUkALA9OEnIJJNMpguR7Tbfl0HleMV9TzLfgu4Ye54ye"
    "xchXJyAMd7vG+NiwKLLd3Yy15LRDJuFO17gbNuZZ8l1/zQwOCsvCCLGqthCDoL/FiVUiCEZ7r3Ql1eliRWgrAhyPb3aCn1AvyZbo"
    "btGWRBGMNrcjolF+e747nShqizjFhUNW+UgtcXD8gbOvtSzbkDiCS8UEhgr5KACxc1WT2TCJro3GzsNJwTYMEwL5cKa4IF1r0LgD"
    "Y/1dMFoIBL6Mq5K30X/T8XxTdfYWg8FgMBgMBoPBYDAYDAaDwWDEG5hGe6aQq3AjHfZmc22cAz4SnQlZFjfi5oNbOkZoBxzzeMbx"
    "Lg6+F4+wtA7BhisIP9iUvCjJrYunBigMicAGyKOa5XMb0334exGkHFm0ZWIE25k3REVuD8xxdtJFMB6W+8HQYXD2ZzgT/N+XqCTk"
    "jz6NGRKiHU0cuvazDkYf8QQL5WcFyEoIjWCsZ7FV0e7HmH0J6S6/g5MFWK/rC8Uzh02LjyWWYMxFvtNRX6cTJcGziPa+xmyTssRz"
    "FZ3iavXUPRTvGNoFM/frLckcwsbetp3ZCMsBUYAk9z9FRTCeathH5Gq39Kmji+J9WqdiAFwRuPGJ9QPsWMOJAmTw18c1ptXKAB7D"
    "hAzXBdTzAKHn1hiQ24awbWAYje0iZp6jS9I5IyZ4lKSdH5yAZYXF359N2PxwDAh+nphXlI2C4G3uqjsRE/ysjZIGuBqQnTyYlWZy"
    "cyMtHOci+GHZafmICYblUQuX1NXQk00ER0anmWDZFYI7bZ2ppgiGY6P5Gr+6wamYQtFFX5ZGmy4kbLo2zEYf8jqhkFCC7yWqyNVM"
    "o02y+igfOj7ug0rXuDE4puTmEd3z3Wm0qSvhwz5xcFgiCMadrwLigPXnpoe/DewqQ0Tmtjsh3YyaSIKRwMYuaY5fByzr3lBULWiY"
    "Rv/1Iuy6Ki5fRlwIrqBxPPNp26cgNcLAsmDStlh8vQkmGDYG6sXAd1TRlp5xGtuSSDAUmYHbQDuEEiHyZ285oprOXyM59H0EdNEl"
    "gMujz0qD36iLri+P27IjLgRDddYil0zGveCNivJEJQXGRkX8Y/xcYgccps9ggvVtvQRP1DvpHPuITRNAt1QMnZa0QEcGJjBQxcoq"
    "hdx+DtGbbI7d15vgUCWQ/BRh910htz3OtBYoE+zP7jpUpdoQ26yGJReNqvkywf5tl93vsDvE9iYQvspngsOxfTlh+69CaKs2kce9"
    "Me5OioRgyJfCLb3S8o3JpMihrwKoGYKfphBtdWKCi9vpTbTTQ1NfJWLbcE8IPjqWqFu5IZWAbi4qgpsR7azWCe1hDU4ZXg/BR1RR"
    "145M8C/beodoa2pAPRcoDqwNtWzziY68Av76VBIQMcE3KKJQUP/xBI/nK+KZYepcE4QPy1u2eT7R1nlM8P+2lanYuC/Z412AgfyL"
    "ccP/HEhcEzLJ43xS4AR6H/aeTPQU61JJQdTLJKf4XgbdGswqFIZg6xKirVwmWN1mc0zntYUpIdjYimjrpVSSkK5AB4b9ihz6hm8/"
    "+DCsdSiW4pehPRMcrH24dXRmwOOtW3CZdHRINrUj2n0+xTAiuhNOsB7ALwiS1zbhf8/CfdhG7C0Gg8FgMBgMBoPBYDAYDAaDwYgv"
    "sJRgE9xMH4on57tjOSO+dVTtOzgc1x4r0Q8XMh6r0XfEkpFZ6TIM7u0diVtrXoW3IWNiKuQAR2DXgBAqqo8LwU4oj7RMUQS1dDHV"
    "eyI7mI7lBQbggayggATvwjALmyiS1k2wwaJ9LT0KuaqOsj4Wao8olDcQ8jcLDoPE9A4h2fhmXAnGaj77DG3ZHMrXjEnauyw67ccw"
    "SCbOFaWdYKFjkEV7oPc81bbjnlc0COPI/UL64oShtZDf4oThEw9DK1u0MQOr3saKYBxvD3lUlof6V09gJfr5WCProOKZ7dZSeaFW"
    "o6IhMOYYj8nYGIWx91okuDrxIxpqKFcY2JTtkZa7CuprK95ngeLZSbYct5RoYJnf0+eKIyGf26r9JPScFLdsRdH+WEWu9nUBPrDv"
    "CR2tbXR7+4iJUvWAetYSL9vKkjPPlehemEZyMx36mqHCgLoGEnqWmhpZk1C8QkPXqDDLFOC478b9aSSYumrgHY1q9PCBvCTRdcBo"
    "HgPneggjf6+hK4/Q1duSQ4fF6b4FnGTKcLWmvnMIfQWmgQ3ZzPQ+DV35hIFtLTl0QqSFs73tmUO8b3NNfVUIfctNDZUFN9Zq6LmD"
    "MLCaJYfKbl+7OI0Ey6Jq+03iyw59IUpV218GTLyaBtBRgfihvGrRoc9J9J+u6JmOcYov0ywbEsFriJhBhoHOzdYPjWPXILuidavs"
    "Qg5CxzzCsLMtOnSTRH991yx7BtahdN+uAofWHofx0bF00ZTQs5h454aa+rJwUmVtXC+t/FpCMVRBvUjx3Cm4mJcezrb8xXwmaQOq"
    "t9bHNXuQQqR9LNgzzeaqAU9PUhhpw4F9FBGpzfiV3o7LIag/sU6xlTjdZnlcLIHvDgfuEXI8UZ7XD+aadN/YG8jwtKY+1UWX99ly"
    "5DkecWkvbA1jZgs1rIjJx9eOGVbrRtqwBhaFKzXW1Kp99/m2HdqMKL/n2Hopi91XaewUshJPFT7rc4esyMCulYROqEZwik8d0Avt"
    "8LDxBZvEFhHjnZ8Nf7iVtLNj+d4fRRDlPyE9ar2Nk8g/eHwhbTXtOl2h8ydcOpZVhDpHuCrhHSYmWhtMHQjLioctbsFB0ZQ6Fgnu"
    "rWhrhp/xHrc6qe3NlQa2Tfexv7sUtwqvcYrLGj9J9C63Ecuv1SbOq4xjkWrWObNUsh0k3w3BPCxVoW3YnG9pieARNrYjoYyRwt7G"
    "mrZBUuJ6G/vS+FXLbmRZbOI8alK1GzMBy3k8302R6gOXSdewQDCUJurhkgs1dc0mbL3BwL4qHh+JF6B+ZgPUJdvdm61rWBfFPm7d"
    "AHpgkf4KoWtaKkbASY0MTxjqzcQMyf0ByV0ExdpQR3VryyQkZTsxOWinoa8WkTMF69eTYkQwbM3tDTH57lgMgqgyUw9gNsdvXM+e"
    "Svx9Px1DziCUzTN4uesJnaNj9hXLUls/CKGdU3DeMhCHu8uFnEmlFit25E7XabyAUDbI4IVaEjofixnBslvFd8XArplED1heRxkV"
    "GjvLwMAsIty50fDFy0mkjIE+2Uz1jTSTW9mRV4ffbPPXAuhhYGRdQucOA50VbAbgMRP0sPWNdXOCqdzqW3UV3hlC6I669Phlw5f/"
    "1NZeMxZOk2FcQD01cMZ82CWrNO3aSth1vO3dkPdLpu0aOp8gdM4yJHgpkVDeSqM3+JCwsY2GXc8QuloH1HMVoWe9idOqKjaYF2no"
    "G6ZYFnQyJPhKRRgwyHr9QWoIcfSuCOhB6IPAT22fOtoozjSdZ9rvL1SQMt1PbjROeIYrjm/sMt18wHHzU0WP08nj+QaKnR9AT027"
    "ymG0ToZtXssbp/gu4R+J55fYGNjrKAx0cMtwHC5/Kpd67mg8adDfx4b7BZYmId092lmLMXLYzWqCe9sDcRNFdU73VcMZeYGHXavQ"
    "jgvQjxDLvwV/mBQgEHOcrdnbpQEOdX2FOzI/+/x7qxXVHfomE11A71LLgl23W7Rpv2P7bgf8Ve2z7LzxISwlyigS/ILiI5shVEwy"
    "MAUMc5eFtQ6rh93ZAUMjlxsfmvLX6+wwtLGqZZtgT/1mjyFPhfdsHRLwMrQBdoVBz+Ku1FlqGNgJ+7A3YfKdX6y3NSfwWJ1MVEye"
    "3PgEu/hyqSiBSWXnO8U1OybiGvdtTKWFWO4UnNRAgDxtFdVxLzYX7ZyMXydMYOAQGBzomo+TwYZp8F83nFDNwcncB/gjgyvo4cqf"
    "dk4cL4hmMBgMBoPBYDAYDAaDwWAwGAwG40gGpvc86qirvJ/GnkouwUU+tui6sKeSSW4bx/uOCSY4oeSWw6wIhwn+/yS4MEBmBxOc"
    "MHJbEYfeFjHBySc3C9NzpGURCILz2XPJIXg8cW6pLSb2M8EJJrc5kf05Df8/E5xgcssQpRjgDFMOE5x8gocQ5HUt9TdMcELJbUAc"
    "KHvK9XdMcEIJllV//85x3fvHBCeT3F5+qwMxwckjtzoeYXXjNdkZXyY4eQTL6kwepGozM8HJIrc9cfD8HsUzTHBCyD0KQ4+y4inl"
    "meDkEzySICrP4zkmOAHk1ifWvHN9PMsEJ4DgJUQxmBpMcPLJzSUIutrn80xwjMmFzYTNJlfMMMHxJri/hBgo9XQiE5x8ciFL40sJ"
    "MZMw79mvUBdgQ+W5pi7JYc9HR3AlJ3r0Ys8zwQwmmMEEM8FONk6yTOUbgsw9kr/txp5P3g+lOS+TmGAGE8xgghlMMIMJZjDBDAaD"
    "wWAwGAwGwxz/BvT12NpJKu8NAAAAAElFTkSuQmCC"
)
# 키 10개가 보여 주는 칸(화면 기준 배경 위치, 줄인 크기 60×100) - 키 R-C 는 그림의 R-C 칸.
NAVER_OFFSETS = [(0, 0), (20, 0), (40, 0), (0, 25), (20, 25), (40, 25), (0, 50), (20, 50), (40, 50), (20, 75)]
NAVER_DRAWN = [0, 2, 6, 1, 3, 7, 8, 5, 9, 4]


def naver_cells(src, offsets=NAVER_OFFSETS, w=20, h=25, bw=60, bh=100):
    return [{"key": str(i), "src": src, "x": x, "y": y, "w": w, "h": h, "bw": bw, "bh": bh} for i, (x, y) in enumerate(offsets)]


class NaverSpriteKeypad(unittest.TestCase):
    def setUp(self):
        self.src = "data:image/png;base64," + NAVER_SPRITE
        self.sheet = Image.open(io.BytesIO(base64.b64decode(NAVER_SPRITE))).convert("RGBA")

    def test_reads_the_real_keypad(self):
        got = pin.read_sprite(naver_cells(self.src), pin.NAVERPAY)
        self.assertEqual(got, {str(i): d for i, d in enumerate(NAVER_DRAWN)})

    def test_reads_a_shuffled_one(self):
        # 그림 속 칸(실제 크기 40×50)을 다른 순서로 다시 붙여, 배치가 바뀐 키패드를 만든다.
        order = [9, 3, 0, 7, 1, 8, 2, 6, 4, 5]
        cells40 = [(x * 2, y * 2) for x, y in NAVER_OFFSETS]
        shuffled = Image.new("RGBA", self.sheet.size, (0, 0, 0, 0))
        for slot, src in enumerate(order):
            sx, sy = cells40[src]
            dx, dy = cells40[slot]
            shuffled.paste(self.sheet.crop((sx, sy, sx + 40, sy + 50)), (dx, dy))
        got = pin.read_sprite(naver_cells(uri(shuffled)), pin.NAVERPAY)
        self.assertEqual(got, {str(slot): NAVER_DRAWN[src] for slot, src in enumerate(order)})

    def test_refuses_what_it_cannot_read(self):
        # 빈 칸(왼쪽 아래), 같은 숫자 두 번, 줄인 크기를 모르고 자른 칸, 쿠페이 글꼴로 읽기 - 아무것도 누르지 않는다.
        for offsets in [NAVER_OFFSETS[:9] + [(0, 75)], NAVER_OFFSETS[:9] + [NAVER_OFFSETS[0]]]:
            with self.assertRaises(RuntimeError):
                pin.read_sprite(naver_cells(self.src, offsets), pin.NAVERPAY)
        with self.assertRaises(RuntimeError):
            pin.read_sprite(naver_cells(self.src, bw=None, bh=None), pin.NAVERPAY)
        with self.assertRaises(RuntimeError):
            pin.read_sprite(naver_cells(self.src), pin.COUPAY)


class SpriteKeypad(unittest.TestCase):
    def setUp(self):
        self.src = "data:image/png;base64," + SPRITE
        self.sheet = Image.open(io.BytesIO(base64.b64decode(SPRITE))).convert("RGBA")

    def test_reads_the_real_keypad(self):
        self.assertEqual(pin.read_sprite(cells(self.src)), {str(i): d for i, d in enumerate(DRAWN)})

    def test_reads_a_shuffled_one(self):
        # 그림 속 칸을 다른 순서로 다시 붙여, 배치가 바뀐 키패드를 만든다.
        order = [3, 0, 9, 1, 8, 2, 7, 4, 6, 5]
        shuffled = Image.new("RGBA", self.sheet.size, (0, 0, 0, 0))
        for slot, src in enumerate(order):
            sx, sy = OFFSETS[src]
            dx, dy = OFFSETS[slot]
            shuffled.paste(self.sheet.crop((sx, sy, sx + 25, sy + 26)), (dx, dy))
        got = pin.read_sprite(cells(uri(shuffled)))
        self.assertEqual(got, {str(slot): DRAWN[src] for slot, src in enumerate(order)})

    def test_a_pixel_off_still_reads(self):
        shifted = [(x + 1, y) for x, y in OFFSETS[:5]] + [(x, y + 1) for x, y in OFFSETS[5:]]
        self.assertEqual(pin.read_sprite(cells(self.src, shifted)), {str(i): d for i, d in enumerate(DRAWN)})

    def test_refuses_what_it_cannot_read(self):
        # 빈 칸(오른쪽 아래), 같은 숫자 두 번, 반쯤 잘린 숫자 - 모두 아무것도 누르지 않는다.
        for offsets in [OFFSETS[:9] + [(75, 52)], OFFSETS[:9] + [OFFSETS[0]]]:
            with self.assertRaises(RuntimeError):
                pin.read_sprite(cells(self.src, offsets))
        with self.assertRaises(RuntimeError):
            pin.read_sprite(cells(self.src, w=8))



class Sites(unittest.TestCase):
    def test_where_a_pin_may_go(self):
        # 키패드가 있는 프레임의 출처로 가린다. 네이버는 결제 호스트만 - 블로그·카페는 누구나 글을 올린다.
        for origin, host in [("https://rocketpay.coupang.com", "coupang.com"), ("https://checkout.coupang.com/x?y", "coupang.com"),
                             ("https://pay.naver.com", "pay.naver.com"), ("https://new-m.pay.naver.com", "pay.naver.com"),
                             ("https://orders.pay.naver.com/order/1", "pay.naver.com")]:
            self.assertEqual((pin.site_of(origin) or {}).get("host"), host, origin)
        for origin in ["https://blog.naver.com", "https://cafe.naver.com", "https://nid.naver.com", "https://naver.com",
                       "https://stdpay.inicis.com", "https://notcoupang.com", "https://pay.naver.com.evil.example", "", "null", "://"]:
            self.assertIsNone(pin.site_of(origin), origin)


# 실제 브라우저(Chromium·Chrome, 시험만을 위한 임시 프로필)로 키패드 페이지를 띄워 누르는 것까지 본다.
# 모든 호스트를 이 컴퓨터로 돌려(host-resolver-rules) 출처만 pay.naver.com·blog.naver.com 처럼 만든다.
CHROMES = [os.environ.get("CHROMIUM", ""), "/usr/bin/chromium", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"]
CHROME = next((c for c in CHROMES if c and os.path.exists(c)), None)

KEYPAD_PAGE = """<!doctype html><meta charset="utf-8"><title>결제 비밀번호</title>
<style>body{font:16px sans-serif} #pad{display:grid;grid-template-columns:repeat(3,60px);gap:6px;width:200px}
#pad button{height:48px} #cover{position:absolute;inset:0;background:transparent;display:none}</style>
<p id="label">결제 비밀번호 6자리를 눌러 주세요</p><div id="pad"></div><div id="cover"></div>
<script>
const q = new URLSearchParams(location.search);
window.entered = [];
function draw() {
  const ds = [0,1,2,3,4,5,6,7,8,9].sort(() => Math.random() - 0.5);
  const pad = document.getElementById('pad');
  pad.innerHTML = '';
  for (const d of ds) {
    const b = document.createElement('button');
    if (q.get('kind') === 'image') { b.style.backgroundImage = 'url(data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==)'; b.dataset.k = 'x' + d; }
    else if (q.get('kind') === 'aria') { b.setAttribute('aria-label', String(d)); b.textContent = ''; }
    else b.textContent = String(d);
    b.addEventListener('click', () => {
      window.entered.push(d);
      document.body.dataset.entered = window.entered.join('');
      if (q.get('shuffle')) draw();
      if (q.get('vanish') && window.entered.length === Number(q.get('vanish'))) pad.remove();
    });
    pad.appendChild(b);
  }
}
draw();
if (q.get('cover')) document.getElementById('cover').style.display = 'block';
if (q.get('notext')) document.getElementById('label').textContent = '숫자를 고르세요';
</script>"""

# 네이버페이 비밀번호 화면과 같은 짜임: 버튼 안의 span.SecureKeyboard_number__* 열 개가 실제 스프라이트를
# 반으로 줄여(60×100) 칸마다 배경 위치로 보여 준다. 누른 키가 그림에서 보여 주는 숫자를 적어 둔다.
NAVER_PAGE = """<!doctype html><meta charset="utf-8"><title>네이버페이</title>
<style>#kb{display:grid;grid-template-columns:repeat(3,130px);gap:0} #kb button{height:63px;border:0;background:#0b9651}
.SecureKeyboard_number__0F2Ti{display:inline-block;width:20px;height:25px;background-size:60px 100px;background-repeat:no-repeat}</style>
<p>비밀번호 입력</p><div id="kb"></div>
<script>
window.entered = [];
const drawn = [0, 2, 6, 1, 3, 7, 8, 5, 9, 4];
const slots = [[1,1],[1,2],[1,3],[2,1],[2,2],[2,3],[3,1],[3,2],[3,3],[4,2]];
const kb = document.getElementById('kb');
for (let r = 1; r <= 4; r++) for (let c = 1; c <= 3; c++) {
  const b = document.createElement('button');
  const i = slots.findIndex(([sr, sc]) => sr === r && sc === c);
  if (i >= 0) {
    const s = document.createElement('span');
    s.className = 'SecureKeyboard_number__0F2Ti SecureKeyboard_key-' + r + '-' + c + '__x';
    s.style.backgroundImage = 'url(data:image/png;base64,SPRITE)';
    s.style.backgroundPosition = (-(c - 1) * 20) + 'px ' + (-(r - 1) * 25) + 'px';
    b.appendChild(s);
    b.addEventListener('click', () => { window.entered.push(drawn[i]); document.body.dataset.entered = window.entered.join(''); });
  } else b.textContent = c === 1 ? '전체삭제' : '←';
  kb.appendChild(b);
}
</script>"""

HOST_PAGE = """<!doctype html><meta charset="utf-8"><title>결제</title><p>결제 비밀번호</p>
<iframe id="f" src="{src}" style="position:absolute;left:120px;top:90px;width:400px;height:420px;border:7px solid #888"></iframe>"""


@unittest.skipUnless(CHROME, "Chromium/Chrome 이 없다")
class KeypadInABrowser(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        import http.server
        import socket
        import subprocess
        import tempfile
        import threading
        import time

        class Pages(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                path, _, query = self.path.partition("?")
                if path == "/keypad":
                    body = KEYPAD_PAGE
                elif path == "/naverpay":
                    body = NAVER_PAGE.replace("SPRITE", NAVER_SPRITE)
                elif path == "/host":
                    from urllib.parse import parse_qs
                    body = HOST_PAGE.replace("{src}", parse_qs(query)["src"][0])
                else:
                    self.send_response(404)
                    self.end_headers()
                    return
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.end_headers()
                self.wfile.write(body.encode())

            def do_POST(self):
                # 보관함의 결제 비밀번호 꺼내기(vault-svc /release-pin) 흉내: 브라우저 토큰이어야 하고, 받은 것을 적어 둔다.
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
                cls.released.append((self.path, self.headers.get("Authorization"), body))
                ok = self.path == "/release-pin" and self.headers.get("Authorization") == "Bearer browser" and \
                    body.get("site") == "pay.naver.com" and body.get("host", "").endswith("pay.naver.com")
                self.send_response(200 if ok else 403)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"pin": "975310"} if ok else {"error": "그 결제의 화면이 아님"}).encode())

            def log_message(self, *_):
                pass

        cls.released = []
        cls.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Pages)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        cls.web = cls.server.server_address[1]
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            cls.cdp = sock.getsockname()[1]
        cls.profile = tempfile.TemporaryDirectory()
        cls.capture = tempfile.TemporaryDirectory()
        pin.CAPTURE_DIR = cls.capture.name
        cls.chrome = subprocess.Popen([
            CHROME, "--headless=new", f"--remote-debugging-port={cls.cdp}", f"--user-data-dir={cls.profile.name}",
            "--no-first-run", "--no-default-browser-check", "--disable-gpu", "--window-size=1000,800",
            "--host-resolver-rules=MAP * 127.0.0.1, EXCLUDE localhost", "about:blank",
        ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(100):
            try:
                urllib.request.urlopen(f"http://127.0.0.1:{cls.cdp}/json/version", timeout=1)
                break
            except OSError:
                time.sleep(0.1)

    @classmethod
    def tearDownClass(cls):
        cls.chrome.terminate()
        cls.chrome.wait(10)
        cls.server.shutdown()
        cls.profile.cleanup()
        cls.capture.cleanup()

    def tearDown(self):
        pin._halted_until = 0.0
        for t in json.load(urllib.request.urlopen(f"http://127.0.0.1:{self.cdp}/json/list")):
            if t.get("type") == "page":
                urllib.request.urlopen(f"http://127.0.0.1:{self.cdp}/json/close/{t['id']}")

    async def open(self, url):
        req = urllib.request.Request(f"http://127.0.0.1:{self.cdp}/json/new?{url}", method="PUT")
        target = json.load(urllib.request.urlopen(req))
        await asyncio.sleep(1.2)
        return target

    async def entered(self, target, expr="window.entered.join('')"):
        import websockets

        async with websockets.connect(target["webSocketDebuggerUrl"], max_size=None) as ws:
            s = pin._Session(ws)
            r = await s.call("Runtime.evaluate", {"expression": expr, "returnByValue": True})
            return r.get("result", {}).get("value")

    async def frame_entered(self, target):
        """탭 안 첫 iframe 이 받은 숫자 - 출처가 달라 바깥 페이지에서는 못 읽으므로 그 프레임에서 읽는다."""
        import websockets

        async with websockets.connect(target["webSocketDebuggerUrl"], max_size=None) as ws:
            s = pin._Session(ws)
            frames = await pin._frames(s)
            ctx = await pin._world(s, frames[1]["id"])
            r = await s.call("Runtime.evaluate", {"expression": "document.body.dataset.entered || ''", "contextId": ctx, "returnByValue": True})
            return r.get("result", {}).get("value")

    def url(self, host, query=""):
        return f"http://{host}:{self.web}/keypad?{query}"

    async def test_enters_on_a_shuffled_keypad(self):
        for kind in ["text", "aria"]:
            t = await self.open(self.url("pay.naver.com", f"kind={kind}"))
            self.assertEqual(await pin.enter(pin="135790", port=self.cdp), "네이버페이 결제 비밀번호를 넣었습니다")
            self.assertEqual(await self.entered(t), "135790", kind)

    async def test_enters_on_the_naver_pay_picture_keypad(self):
        # 2026-10-30 00:07 에 넣지 못한 그 키패드의 짜임과 그림 그대로.
        t = await self.open(f"http://pay.naver.com:{self.web}/naverpay")
        self.assertEqual(await pin.enter(pin="135790", port=self.cdp), "네이버페이 결제 비밀번호를 넣었습니다")
        self.assertEqual(await self.entered(t), "135790")

    async def test_reads_again_when_the_keypad_reshuffles_after_each_press(self):
        t = await self.open(self.url("orders.pay.naver.com", "shuffle=1"))
        await pin.enter(pin="902468", port=self.cdp)
        self.assertEqual(await self.entered(t), "902468")

    async def test_a_keypad_in_a_frame_of_the_same_site(self):
        inner = self.url("pay.naver.com", "shuffle=1")
        t = await self.open(f"http://new-m.pay.naver.com:{self.web}/host?src={inner}")
        await pin.enter(pin="314159", port=self.cdp)
        self.assertEqual(await self.frame_entered(t), "314159")

    async def iframe_entered(self):
        """따로 떠 있는(다른 사이트 화면 속) 프레임이 받은 숫자 - 그 프레임에 직접 붙어 읽는다."""
        import websockets

        frame = next(t for t in json.load(urllib.request.urlopen(f"http://127.0.0.1:{self.cdp}/json/list")) if t.get("type") == "iframe")
        async with websockets.connect(frame["webSocketDebuggerUrl"], max_size=None) as ws:
            s = pin._Session(ws)
            r = await s.call("Runtime.evaluate", {"expression": "document.body.dataset.entered || ''", "returnByValue": True})
            return r.get("result", {}).get("value")

    async def test_a_keypad_in_a_payment_frame_of_another_site(self):
        # 쇼핑몰 화면 위 결제창 안의 결제 화면: 다른 사이트 프레임이라 탭에서는 평가되지 않는다 - 그 프레임에
        # 직접 붙어, 그 프레임 안의 좌표로 누른다.
        await self.open(f"http://shop.example:{self.web}/host?src={self.url('pay.naver.com', 'shuffle=1')}")
        self.assertEqual(await pin.enter(pin="271828", port=self.cdp), "네이버페이 결제 비밀번호를 넣었습니다")
        self.assertEqual(await self.iframe_entered(), "271828")

    async def test_does_not_enter_where_it_should_not(self):
        # 블로그(같은 naver.com 이라도 누구나 쓰는 곳), '비밀번호' 글자가 없는 화면, 덮인 키패드, 그림이라
        # 읽지 못하는 키패드, 다른 사이트 화면 속 블로그 프레임 - 어느 것도 누르지 않는다.
        cases = [self.url("blog.naver.com"), self.url("pay.naver.com", "notext=1"), self.url("pay.naver.com", "cover=1"),
                 f"http://shop.example:{self.web}/host?src={self.url('blog.naver.com')}",
                 self.url("pay.naver.com", "kind=image")]
        for url in cases:
            t = await self.open(url)
            with self.assertRaises(RuntimeError, msg=url):
                await pin.enter(pin="135790", port=self.cdp)
            got = await self.entered(t, "(window.entered || []).join('') + '|' + "
                                        "(document.getElementById('f') ? 'frame' : '')")
            self.assertTrue(got.startswith("|"), f"{url}: {got}")
            self.tearDown()
        # 읽지 못한 키패드는 모양을 남긴다.
        kept = os.listdir(self.capture.name)
        self.assertTrue(kept)
        last = os.path.join(self.capture.name, sorted(kept)[-1])
        self.assertTrue(os.path.exists(os.path.join(last, "targets.json")))
        # 마지막(그림 키패드)은 그 프레임의 키 모양과 그림까지 남긴다 - 다음에 읽는 법을 만들 재료.
        outlines = [json.load(open(os.path.join(last, f))) for f in os.listdir(last) if f.endswith(".json") and f != "targets.json"]
        frame = [o for o in outlines if "pay.naver.com" in o.get("origin", "")]
        self.assertTrue(frame and len(frame[0]["keys"]) >= 10, outlines)
        self.assertTrue(any(f.endswith(".png") and "-i" in f for f in os.listdir(last)), os.listdir(last))

    async def test_stops_and_holds_when_the_keypad_goes_away(self):
        # 세 자리를 누른 뒤 키패드가 사라지면 거기서 멈추고, 한동안은 다시 넣지 않는다(이어 넣으면 틀린다).
        t = await self.open(self.url("pay.naver.com", "vanish=3"))
        with self.assertRaisesRegex(RuntimeError, "3자리에서 멈췄습니다"):
            await pin.enter(pin="246802", port=self.cdp)
        self.assertEqual(await self.entered(t), "246")
        await self.open(self.url("pay.naver.com"))
        with self.assertRaisesRegex(RuntimeError, "멈춘 키패드"):
            await pin.enter(pin="246802", port=self.cdp)

    async def test_the_pin_must_be_set(self):
        # 보관함에도 환경변수에도 없으면 누르지 않고, 대화의 저장 카드로 넣어 달라고 한다.
        t = await self.open(self.url("pay.naver.com"))
        os.environ.pop("NAVER_PAY_PIN", None)
        with self.assertRaisesRegex(RuntimeError, "save_payment_pin"):
            await pin.enter(port=self.cdp)
        self.assertEqual(await self.entered(t), "")

    async def test_the_pin_comes_from_the_vault_for_that_payment_only(self):
        # 대화의 카드(save_payment_pin)로 넣은 비밀번호를 보관함에서 꺼낸다 - 키패드가 있는 프레임의 호스트와 함께.
        os.environ.pop("NAVER_PAY_PIN", None)
        pin.VAULT_URL, pin.VAULT_TOKEN = f"http://127.0.0.1:{self.web}", "browser"
        try:
            self.released.clear()
            t = await self.open(f"http://orders.pay.naver.com:{self.web}/naverpay")
            self.assertEqual(await pin.enter(port=self.cdp), "네이버페이 결제 비밀번호를 넣었습니다")
            self.assertEqual(await self.entered(t), "975310")
            self.assertEqual([(p, b["site"], b["host"]) for p, _, b in self.released],
                             [("/release-pin", "pay.naver.com", "orders.pay.naver.com")])
        finally:
            pin.VAULT_URL, pin.VAULT_TOKEN = "http://vault:8105", ""


if __name__ == "__main__":
    unittest.main()
